# DragonflyDB with Redis Orchestrator

This guide shows how to deploy [DragonflyDB](https://www.dragonflydb.io/) with the Redis Orchestrator for automatic master election and failover.

## What is DragonflyDB?

DragonflyDB is a modern, high-performance, Redis-compatible in-memory datastore. It's designed as a drop-in replacement for Redis with:
- Full Redis API compatibility
- 25x higher throughput than Redis
- Vertical scalability (multi-threaded)
- Lower memory footprint
- Native replication support

The Redis Orchestrator works with DragonflyDB because it uses the standard Redis protocol for replication commands.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                        │
│                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │   Pod 0      │  │   Pod 1      │  │   Pod 2      │     │
│  │              │  │              │  │              │     │
│  │ ┌──────────┐ │  │ ┌──────────┐ │  │ ┌──────────┐ │     │
│  │ │Dragonfly │ │  │ │Dragonfly │ │  │ │Dragonfly │ │     │
│  │ │ (Master) │ │  │ │ (Replica)│ │  │ │ (Replica)│ │     │
│  │ └────┬─────┘ │  │ └────┬─────┘ │  │ └────┬─────┘ │     │
│  │      │       │  │      │       │  │      │       │     │
│  │ ┌────▼─────┐ │  │ ┌────▼─────┐ │  │ ┌────▼─────┐ │     │
│  │ │Orchestr. │◄├──┤►│Orchestr. │◄├──┤►│Orchestr. │ │     │
│  │ └──────────┘ │  │ └──────────┘ │  │ └──────────┘ │     │
│  │ redis-role:  │  │              │  │              │     │
│  │   master ✓   │  │              │  │              │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
│         ▲                                                   │
│         │                                                   │
│  ┌──────┴────────────────────────┐                        │
│  │  Service: dragonfly-master    │                        │
│  │  Selector: redis-role=master  │                        │
│  └───────────────────────────────┘                        │
└─────────────────────────────────────────────────────────────┘
```

## Prerequisites

- Kubernetes 1.20+
- Helm 3.0+
- kubectl configured

## Quick Start

### 1. Add DragonflyDB Helm Repository

```bash
helm repo add dragonfly https://dragonflydb.github.io/dragonfly-helm-charts/
helm repo update
```

### 2. Create Namespace

```bash
kubectl create namespace dragonfly
```

### 3. Apply RBAC Manifests

The orchestrator needs permissions to manage pod labels:

```bash
kubectl apply -f rbac.yaml -n dragonfly
```

### 4. Apply Service Manifests

Create the master service and headless service:

```bash
kubectl apply -f service.yaml -n dragonfly
```

### 5. Install DragonflyDB with Orchestrator

```bash
helm install dragonfly dragonfly/dragonfly \
  --namespace dragonfly \
  --values values.yaml
```

### 6. Verify Installation

```bash
# Check pods
kubectl get pods -n dragonfly

# Should see 3 pods with 2/2 containers ready:
# NAME          READY   STATUS    RESTARTS   AGE
# dragonfly-0   2/2     Running   0          2m
# dragonfly-1   2/2     Running   0          2m
# dragonfly-2   2/2     Running   0          2m

# Check which pod is master
kubectl get pods -n dragonfly -l redis-role=master

# Should see ONE pod with the master label
```

### 7. Test Connection

```bash
# Port-forward to the master service
kubectl port-forward -n dragonfly svc/dragonfly-master 6379:6379

# In another terminal, test with redis-cli
redis-cli -h localhost -p 6379 PING
# Output: PONG

# Test replication
redis-cli -h localhost -p 6379 INFO replication
```

## Configuration Options

### Replica Count

Adjust the number of DragonflyDB instances:

```yaml
# values.yaml
replicaCount: 5  # Deploy 5 instances
```

Recommended:
- **Production**: 3-5 replicas
- **Testing**: 2-3 replicas
- **Development**: 1 replica (no HA)

### Storage

Configure persistent storage:

```yaml
storage:
  enabled: true
  storageClassName: "fast-ssd"  # Use specific storage class
  requests: 50Gi  # Increase storage size
```

### Authentication

Enable password authentication:

1. Create the password secret:
```bash
kubectl apply -f password-secret.yaml -n dragonfly
```

2. Update `values.yaml`:
```yaml
# Uncomment in extraArgs:
extraArgs:
  - --requirepass=$(REDIS_PASSWORD)
  - --masterauth=$(REDIS_PASSWORD)

# Uncomment in extraContainers (orchestrator):
env:
  - name: REDIS_PASSWORD
    valueFrom:
      secretKeyRef:
        name: dragonfly-password
        key: password
```

3. Update the orchestrator args:
```yaml
args:
  - --redis-password=$(REDIS_PASSWORD)
```

### Debug Logging

Enable verbose orchestrator logging:

```yaml
# In extraContainers args:
args:
  - --debug
```

### Resource Limits

Adjust resources based on your workload:

```yaml
# DragonflyDB resources
resources:
  requests:
    cpu: 500m
    memory: 1Gi
  limits:
    cpu: 4000m
    memory: 8Gi

# Orchestrator resources (in extraContainers)
resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    cpu: 200m
    memory: 128Mi
```

## Upgrading

### Upgrade Helm Chart

```bash
helm upgrade dragonfly dragonfly/dragonfly \
  --namespace dragonfly \
  --values values.yaml
```

### Upgrade Orchestrator

Update the image tag in `values.yaml`:

```yaml
extraContainers:
  - name: orchestrator
    image: sindef/redis-orchestrator:v1.0.2  # New version
```

Then upgrade:

```bash
helm upgrade dragonfly dragonfly/dragonfly \
  --namespace dragonfly \
  --values values.yaml
```

## Testing Failover

### Automatic Failover Test

```bash
# 1. Identify current master
MASTER=$(kubectl get pods -n dragonfly -l redis-role=master -o jsonpath='{.items[0].metadata.name}')
echo "Current master: $MASTER"

# 2. Write test data
kubectl exec -n dragonfly $MASTER -c dragonfly -- \
  redis-cli SET test_key "failover_test_$(date +%s)"

# 3. Delete master pod
kubectl delete pod -n dragonfly $MASTER

# 4. Wait for new master election (15-30 seconds)
sleep 20

# 5. Check new master
NEW_MASTER=$(kubectl get pods -n dragonfly -l redis-role=master -o jsonpath='{.items[0].metadata.name}')
echo "New master: $NEW_MASTER"

# 6. Verify data survived
kubectl exec -n dragonfly $NEW_MASTER -c dragonfly -- \
  redis-cli GET test_key
```

### View Orchestrator Logs

```bash
# Watch election process
kubectl logs -f dragonfly-0 -c orchestrator -n dragonfly

# View logs from all orchestrators
kubectl logs -l app.kubernetes.io/name=dragonfly -c orchestrator -n dragonfly
```

## Monitoring

### Check Replication Status

```bash
# On master
kubectl exec -n dragonfly -c dragonfly \
  $(kubectl get pods -n dragonfly -l redis-role=master -o jsonpath='{.items[0].metadata.name}') \
  -- redis-cli INFO replication
```

### Orchestrator Health

```bash
# Check orchestrator health endpoint
kubectl port-forward -n dragonfly dragonfly-0 8080:8080

# In another terminal
curl http://localhost:8080/health
# Output: OK

# Get orchestrator state (JSON)
curl http://localhost:8080/state
```

### Prometheus Monitoring

Enable ServiceMonitor for Prometheus:

```yaml
# values.yaml
serviceMonitor:
  enabled: true
  interval: 10s
```

## Multi-Site Deployment

For deploying across multiple Kubernetes clusters, see [MULTI-SITE.md](../../MULTI-SITE.md).

**Key considerations:**
- Use the same Helm values across both sites
- Enable debug logging to see which pod is elected
- Ensure network connectivity via service mesh (Istio/Linkerd)
- Pod UIDs will ensure deterministic master election

## Troubleshooting

### No Master Elected

```bash
# Check orchestrator logs
kubectl logs dragonfly-0 -c orchestrator -n dragonfly | grep "ELECTED"

# Check RBAC permissions
kubectl auth can-i update pods \
  --as=system:serviceaccount:dragonfly:dragonfly-orchestrator \
  -n dragonfly

# Should output: yes
```

### Split-Brain Detection

```bash
# Check if multiple masters exist
kubectl get pods -n dragonfly -l redis-role=master

# Should only see ONE pod
# If you see multiple, check orchestrator logs:
kubectl logs -l app.kubernetes.io/name=dragonfly -c orchestrator -n dragonfly | grep "SPLIT BRAIN"
```

### Replication Not Working

```bash
# Check DragonflyDB replication status
kubectl exec -n dragonfly dragonfly-0 -c dragonfly -- redis-cli INFO replication

# Check if replica is connected
kubectl exec -n dragonfly dragonfly-1 -c dragonfly -- redis-cli INFO replication | grep master_link_status

# Should show: master_link_status:up
```

### Pods Not Starting

```bash
# Check pod events
kubectl describe pod dragonfly-0 -n dragonfly

# Check orchestrator logs
kubectl logs dragonfly-0 -c orchestrator -n dragonfly

# Check DragonflyDB logs
kubectl logs dragonfly-0 -c dragonfly -n dragonfly
```

## Comparison: DragonflyDB vs Redis

| Feature | DragonflyDB | Redis |
|---------|-------------|-------|
| Throughput | 25x higher | Baseline |
| Memory Usage | 30% lower | Baseline |
| Threading | Multi-threaded | Single-threaded |
| Replication | Native | Native |
| API Compatibility | 100% Redis | 100% |
| Cluster Mode | Coming soon | Available |
| License | BSL 1.1 | BSD 3-Clause |

**When to use DragonflyDB:**
- High throughput requirements (1M+ ops/sec)
- Large datasets (100GB+)
- Need to reduce Redis infrastructure costs
- Want vertical scalability

**When to use Redis:**
- Need Redis Cluster (sharding)
- Require specific Redis modules
- Established Redis deployment

## Performance Tuning

### DragonflyDB Configuration

```yaml
extraArgs:
  # Increase max memory
  - --maxmemory=8gb
  
  # Set eviction policy
  - --maxmemory-policy=allkeys-lru
  
  # Enable TLS
  # - --tls
  
  # Bind to all interfaces (default)
  - --bind=0.0.0.0
  
  # Snapshot configuration
  - --save=60:1000
  - --dbfilename=dump.rdb
```

### Orchestrator Tuning

```yaml
args:
  # Faster failover detection (5s instead of 15s)
  - --sync-interval=5s
  
  # More frequent health checks
```

## Uninstalling

```bash
# Delete Helm release
helm uninstall dragonfly -n dragonfly

# Delete services
kubectl delete -f service.yaml -n dragonfly

# Delete RBAC
kubectl delete -f rbac.yaml -n dragonfly

# Delete PVCs (if you want to remove data)
kubectl delete pvc -l app.kubernetes.io/name=dragonfly -n dragonfly

# Delete namespace
kubectl delete namespace dragonfly
```

## Additional Resources

- [DragonflyDB Documentation](https://www.dragonflydb.io/docs)
- [DragonflyDB Helm Chart](https://github.com/dragonflydb/dragonfly/tree/main/contrib/charts/dragonfly)
- [Redis Orchestrator Documentation](../../README.md)
- [Multi-Site Deployment Guide](../../MULTI-SITE.md)

## Support

For issues related to:
- **DragonflyDB**: https://github.com/dragonflydb/dragonfly/issues
- **Redis Orchestrator**: https://github.com/sindef/redis-orchestrator/issues
- **Helm Chart**: https://github.com/dragonflydb/dragonfly/issues

## License

- DragonflyDB: BSL 1.1 (Business Source License)
- Redis Orchestrator: MIT License

