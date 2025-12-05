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
2. Sort by:
   - Primary: Startup timestamp (oldest first)
   - Tie-breaker: Pod name (lexicographically)
3. Elect the instance with the oldest startup time
4. If tie, elect the one with the "smallest" pod name

**Why this works for even replicas:**
- Deterministic: All instances reach the same conclusion
- No quorum needed: Works with 2, 3, 4, or any number of replicas
- Stable: The oldest pod is always preferred (prevents flapping)

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

1. **Build the Docker image:**

```bash
make docker VERSION=v1.0.0
```

2. **Deploy to Kubernetes:**

```bash
kubectl apply -f deploy/statefulset.yaml
```

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
# Check orchestrator logs
kubectl logs -l app=redis -c orchestrator

# Check pod labels
kubectl get pods -l app=redis --show-labels

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

