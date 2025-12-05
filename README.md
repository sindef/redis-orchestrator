# Redis Orchestrator

A cloud-native Redis orchestration agent that provides automatic master election and failover for Redis deployments in Kubernetes, designed as a lightweight alternative to Redis Sentinel.

## Features

- **Kubernetes-Native**: Runs as a sidecar container alongside Redis pods
- **Automatic Master Election**: Deterministic leader election using startup timestamps
- **Even Replica Support**: Works with any number of replicas (no quorum required)
- **Split-Brain Resolution**: Automatically resolves multiple master scenarios
- **TLS Support**: Full support for TLS-encrypted Redis connections
- **Multi-Site Ready**: Designed for multi-datacenter deployments with service mesh
- **Zero Dependencies**: No external coordination service required
- **Pod Label Management**: Automatically sets `redis-role=master` label for service routing

## Architecture

### How It Works

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                        │
│                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │   Pod 0      │  │   Pod 1      │  │   Pod 2      │     │
│  │              │  │              │  │              │     │
│  │ ┌──────────┐ │  │ ┌──────────┐ │  │ ┌──────────┐ │     │
│  │ │  Redis   │ │  │ │  Redis   │ │  │ │  Redis   │ │     │
│  │ │ (Master) │ │  │ │ (Replica)│ │  │ │ (Replica)│ │     │
│  │ └────┬─────┘ │  │ └────┬─────┘ │  │ └────┬─────┘ │     │
│  │      │       │  │      │       │  │      │       │     │
│  │ ┌────▼─────┐ │  │ ┌────▼─────┐ │  │ ┌────▼─────┐ │     │
│  │ │Orchestr. │◄├──┤►│Orchestr. │◄├──┤►│Orchestr. │ │     │
│  │ │(HTTP API)│ │  │ │(HTTP API)│ │  │ │(HTTP API)│ │     │
│  │ └──────────┘ │  │ └──────────┘ │  │ └──────────┘ │     │
│  │ redis-role:  │  │              │  │              │     │
│  │   master ✓   │  │              │  │              │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
│         ▲                                                   │
│         │                                                   │
│  ┌──────┴──────────────────────────────┐                  │
│  │  Service: redis                     │                  │
│  │  Selector: redis-role=master        │                  │
│  └─────────────────────────────────────┘                  │
└─────────────────────────────────────────────────────────────┘
```

### State Synchronization Flow

Every 15 seconds (configurable), each orchestrator:

1. **Checks Local Redis State** via `INFO replication`
2. **Discovers Peers** using Kubernetes API (pods with matching labels)
3. **Queries Peer State** via HTTP API (port 8080)
4. **Reconciles State** and takes action if needed

### Leader Election Algorithm

When no master is detected:

1. Collect all healthy Redis instances
2. Sort by three-tier tie-breaking:
   - **Primary:** Startup timestamp (oldest first)
   - **Secondary:** Pod name (lexicographically smallest)
   - **Tertiary:** Pod UID (lexicographically smallest) - for multi-site
3. Elect the instance that ranks first after sorting

**Why this works for even replicas AND multi-site:**
- **Deterministic:** All instances independently reach the same conclusion
- **No quorum needed:** Works with 2, 3, 4, or any number of replicas
- **Stable:** The oldest pod is always preferred (prevents flapping)
- **Multi-site safe:** Pod UID prevents ambiguity when names are identical
  - Example: `redis-0` in Site1 and `redis-0` in Site2 → UID breaks the tie

**Multi-Site Scenario:**
If you have `redis-0` in both Site A and Site B with identical startup times:
```
Site A: redis-0 (UID: zzz-abc) 
Site B: redis-0 (UID: aaa-xyz) ← Elected (UID is smaller)
```

See [MULTI-SITE.md](MULTI-SITE.md) for detailed multi-site deployment guide.

### Split-Brain Resolution

When multiple masters are detected:

1. Keep the master with the oldest startup time
2. Demote all other masters to replicas
3. Update pod labels accordingly

### Pod Label Management

The orchestrator manages the `redis-role=master` label:

- **Set on master**: When a pod is promoted
- **Removed from others**: When demoted to replica
- **Service selector**: The Redis service uses `redis-role=master` to route traffic

This enables automatic DNS updates through Kubernetes services.

## Deployment

### Prerequisites

- Kubernetes 1.20+
- Service account with pod `get`, `list`, `watch`, `update`, `patch` permissions
- Redis 6.0+ (for `REPLICAOF` command)

### Quick Start

### Redis Deployment

1. **Build the Docker image:**

```bash
make docker VERSION=v1.0.0
```

2. **Deploy to Kubernetes:**

```bash
kubectl apply -f deploy/statefulset.yaml
```

### DragonflyDB Deployment

For DragonflyDB (modern Redis-compatible datastore with 25x higher throughput):

```bash
cd deploy/dragonflydb
kubectl apply -f rbac.yaml -n dragonfly
kubectl apply -f service.yaml -n dragonfly

helm install dragonfly dragonfly/dragonfly \
  --namespace dragonfly \
  --values values.yaml
```

See [DRAGONFLYDB.md](DRAGONFLYDB.md) for complete DragonflyDB deployment guide.

3. **Verify deployment:**

```bash
# Check pods
kubectl get pods -l app=redis

# Check which pod is master
kubectl get pods -l app=redis,redis-role=master

# Test Redis connection
kubectl run -it --rm redis-cli --image=redis:7-alpine --restart=Never -- \
  redis-cli -h redis -a changeme123 PING
```

### Configuration Options

| Flag | Environment | Default | Description |
|------|------------|---------|-------------|
| `--redis-host` | - | `localhost` | Redis host |
| `--redis-port` | - | `6379` | Redis port |
| `--redis-password` | `REDIS_PASSWORD` | - | Redis password |
| `--redis-tls` | - | `false` | Enable TLS |
| `--redis-service` | - | `redis` | Service name for replicas |
| `--sync-interval` | - | `15s` | State sync interval |
| `--pod-name` | `POD_NAME` | - | Pod name (required) |
| `--namespace` | `POD_NAMESPACE` | - | Namespace (required) |
| `--label-selector` | - | `app=redis` | Label selector for pods |
| `--debug` | - | `false` | Enable verbose debug logging (use `--debug=true`) |

### TLS Configuration

For TLS-encrypted Redis:

```bash
# Create TLS secret
kubectl create secret tls redis-tls \
  --cert=redis.crt \
  --key=redis.key

# Apply TLS configuration
kubectl apply -f deploy/tls-example.yaml
```

## Multi-Site Deployment

For multi-datacenter deployments with service mesh:

```bash
kubectl apply -f deploy/multi-site-example.yaml
```

**Requirements:**
- Service mesh (Istio, Linkerd, etc.) for cross-cluster discovery
- Network connectivity between sites
- Shared service namespace or exported services

**Considerations:**
- Network latency affects replication lag
- Test failover thoroughly
- Monitor split-brain scenarios during network partitions
- Consider using Redis Cluster for large datasets

## Debug Logging

Enable verbose logging to understand election decisions:

```bash
# Add to container args
args:
- --debug=true
- --redis-host=localhost
- --redis-port=6379
```

**Debug output includes:**
- State sync cycle details
- All discovered peers with UIDs
- Election candidates and ranking
- Decision reasoning (why a specific pod was elected)
- Promotion/demotion actions with full context
- Split-brain detection and resolution details

**Example debug output:**
```
============================================
NO MASTER DETECTED - Starting election
Election candidates count=3
Candidate rank=1 pod=redis-0 uid=abc-123 startupTime=2025-12-05T10:00:00Z
Candidate rank=2 pod=redis-1 uid=def-456 startupTime=2025-12-05T10:05:00Z
Candidate rank=3 pod=redis-2 uid=ghi-789 startupTime=2025-12-05T10:10:00Z

ELECTED MASTER pod=redis-0 uid=abc-123 reason=oldest startup time
WE ARE THE ELECTED MASTER - Promoting
```

## Monitoring

The orchestrator exposes the following endpoints:

- **`/health`**: Health check endpoint
- **`/state`**: Current pod state (JSON)

### Prometheus Metrics (Future Enhancement)

Consider adding metrics for:
- Master election events
- Split-brain detections
- Replication lag
- Peer discovery failures

## Failure Scenarios

### Master Pod Failure

1. Master pod becomes unhealthy/deleted
2. Orchestrators detect no master after sync interval (15s)
3. New master elected from healthy replicas
4. Service automatically routes to new master

**Recovery time:** ~15-30 seconds

### Network Partition

**Scenario:** Site 1 and Site 2 lose connectivity

1. Each site may elect its own master (split-brain)
2. When connectivity restored, orchestrators detect multiple masters
3. Oldest master retained, others demoted
4. Manual intervention may be needed to resolve data conflicts

**Recommendation:** Use Redis Cluster for multi-site or implement conflict resolution

### Even Number of Replicas

The orchestrator handles even replicas gracefully:
- 2 replicas: Oldest becomes master
- 4 replicas: Oldest becomes master
- No quorum issues like with Raft-based systems

### All Pods Restart Simultaneously

1. All pods start at approximately the same time
2. Timestamp tie-breaker uses pod name
3. Deterministic election (e.g., `redis-0` if timestamps identical)

## Limitations

1. **Data Conflict Resolution**: Does not handle conflicts from split-brain scenarios - manual intervention required
2. **Replication Lag**: No automatic monitoring or alerting (add externally)
3. **Cross-Region Latency**: Not optimized for high-latency multi-region deployments
4. **No Automatic Scaling**: Does not trigger pod autoscaling
5. **Single Redis Master**: Does not support multiple masters (Redis limitation)

## Comparison with Redis Sentinel

| Feature | Redis Orchestrator | Redis Sentinel |
|---------|-------------------|----------------|
| Deployment | Sidecar (K8s-native) | Separate processes |
| Dependencies | Kubernetes only | None |
| Quorum | Not required | Required (odd number) |
| Even replicas | Supported | Not recommended |
| Pod labels | Automatic | Manual/external |
| Service discovery | Kubernetes API | Redis protocol |
| Multi-site | Service mesh | Redis Sentinel protocol |

## Development

### Building Locally

```bash
go mod download
go build -o redis-orchestrator .
```

### Running Tests

```bash
go test -v ./...
```

### Local Testing

```bash
# Start local Redis
docker run -d --name redis -p 6379:6379 redis:7-alpine

# Run orchestrator (requires Kubernetes context)
make run-local
```

## Roadmap

- [ ] Prometheus metrics export
- [ ] Graceful shutdown with state preservation
- [ ] Replication lag monitoring
- [ ] Automatic alerts for split-brain
- [ ] Support for Redis Cluster
- [ ] Web UI for state visualization
- [ ] Integration tests with chaos engineering
- [ ] Helm chart

## Contributing

Contributions welcome! Please open an issue or PR.

## License

MIT License - see LICENSE file for details

## Security Considerations

1. **RBAC**: Ensure service account has minimal required permissions
2. **Network Policies**: Restrict orchestrator HTTP port (8080) to pod network
3. **Redis Auth**: Always use strong passwords
4. **TLS**: Enable TLS for production deployments
5. **Pod Security**: Run as non-root user (UID 65534)

## Troubleshooting

### No master elected

```bash
# Check orchestrator logs with debug info
kubectl logs -l app=redis -c orchestrator | grep -E "ELECTED|NO MASTER"

# Enable debug logging (add to args in StatefulSet)
kubectl edit statefulset redis
# Add: - --debug=true
# Then restart:
kubectl rollout restart statefulset/redis

# Check pod labels
kubectl get pods -l app=redis --show-labels

# Check pod UIDs (important for multi-site)
kubectl get pods -o custom-columns=NAME:.metadata.name,UID:.metadata.uid

# Manually check Redis state
kubectl exec redis-0 -- redis-cli INFO replication
```

### Split-brain not resolving

```bash
# Check peer connectivity
kubectl exec redis-0 -c orchestrator -- wget -O- http://redis-1.redis-headless:8080/state

# Check RBAC permissions
kubectl auth can-i update pods --as=system:serviceaccount:default:redis-orchestrator
```

### Replication not working

```bash
# Check Redis auth
kubectl exec redis-0 -- redis-cli -a password AUTH

# Check service DNS
kubectl exec redis-0 -- nslookup redis

# Check Redis logs
kubectl logs redis-0 -c redis
```

## Credits

Inspired by Redis Sentinel but built for Kubernetes-native deployments.

## Support

For issues, questions, or contributions, please open an issue on GitHub.

