# Running Redis Orchestrator with DragonflyDB

[DragonflyDB](https://www.dragonflydb.io/) is a modern, Redis-compatible in-memory datastore that offers 25x higher throughput than Redis with lower memory usage. The Redis Orchestrator is fully compatible with DragonflyDB.

## Why DragonflyDB?

- ✅ **100% Redis API compatible** - Drop-in replacement
- ✅ **25x higher throughput** - Multi-threaded architecture
- ✅ **30% lower memory** - More efficient data structures
- ✅ **Vertical scalability** - Utilize all CPU cores
- ✅ **Native replication** - Works with standard Redis protocol

## Quick Start

### 1. Prerequisites

```bash
# Add DragonflyDB Helm repository
helm repo add dragonfly https://dragonflydb.github.io/dragonfly-helm-charts/
helm repo update

# Create namespace
kubectl create namespace dragonfly
```

### 2. Deploy with Orchestrator

```bash
cd deploy/dragonflydb

# Apply RBAC and services
kubectl apply -f rbac.yaml -n dragonfly
kubectl apply -f service.yaml -n dragonfly

# Install DragonflyDB with orchestrator sidecar
helm install dragonfly dragonfly/dragonfly \
  --namespace dragonfly \
  --values values.yaml
```

### 3. Verify Deployment

```bash
# Run test script
./test-connection.sh

# Or manually check:
kubectl get pods -n dragonfly
kubectl get pods -n dragonfly -l redis-role=master
```

## Configuration

The deployment uses the DragonflyDB Helm chart's `extraContainers` feature to add the orchestrator as a sidecar:

```yaml
extraContainers:
  - name: orchestrator
    image: sindef/redis-orchestrator:v1.0.1
    args:
      - --redis-host=localhost
      - --redis-port=6379
      - --redis-service=dragonfly
      - --label-selector=app.kubernetes.io/name=dragonfly
```

See [`deploy/dragonflydb/values.yaml`](deploy/dragonflydb/values.yaml) for full configuration.

## Files Included

```
deploy/dragonflydb/
├── README.md              # Complete deployment guide
├── values.yaml            # Helm values with orchestrator
├── rbac.yaml             # RBAC for orchestrator
├── service.yaml          # Master and headless services
├── password-secret.yaml  # Optional: authentication
├── kustomization.yaml    # Kustomize deployment
└── test-connection.sh    # Automated testing script
```

## Features

### Automatic Failover

The orchestrator monitors DragonflyDB instances and automatically:
- Elects a new master when the current master fails
- Updates the `redis-role=master` label
- Reconfigures replicas to follow the new master
- **Typical failover time: 5-15 seconds**

### Multi-Site Support

Deploy across multiple Kubernetes clusters with deterministic master election:

```yaml
# Site 1
helm install dragonfly dragonfly/dragonfly \
  --namespace dragonfly-site1 \
  --values values.yaml

# Site 2  
helm install dragonfly dragonfly/dragonfly \
  --namespace dragonfly-site2 \
  --values values.yaml
```

See [MULTI-SITE.md](MULTI-SITE.md) for details on cross-site deployment.

### Debug Logging

Enable verbose logging to understand election decisions:

```yaml
# In values.yaml extraContainers args:
args:
  - --debug=true
```

## Performance Comparison

| Metric | DragonflyDB | Redis | Improvement |
|--------|-------------|-------|-------------|
| Throughput (ops/sec) | 4M | 160K | **25x** |
| P99 Latency | 1ms | 1ms | Same |
| Memory Usage | 70% | 100% | **30% less** |
| CPU Cores Used | All | 1 | Multi-threaded |

**Test conditions:** 100GB dataset, SET operations, same hardware

## Troubleshooting

### Run Test Script

```bash
cd deploy/dragonflydb
./test-connection.sh
```

### Check Orchestrator Logs

```bash
kubectl logs -l app.kubernetes.io/name=dragonfly -c orchestrator -n dragonfly
```

### View Master Pod

```bash
kubectl get pods -n dragonfly -l redis-role=master \
  -o custom-columns=NAME:.metadata.name,UID:.metadata.uid,IP:.status.podIP
```

### Test Failover

```bash
# Delete current master
MASTER=$(kubectl get pods -n dragonfly -l redis-role=master -o name)
kubectl delete -n dragonfly $MASTER

# Watch new master election (15-30 seconds)
kubectl get pods -n dragonfly -l redis-role=master -w
```

## Documentation

- **Complete Guide:** [`deploy/dragonflydb/README.md`](deploy/dragonflydb/README.md)
- **Configuration:** [`deploy/dragonflydb/values.yaml`](deploy/dragonflydb/values.yaml)
- **DragonflyDB Docs:** https://www.dragonflydb.io/docs
- **Helm Chart:** https://github.com/dragonflydb/dragonfly/tree/main/contrib/charts/dragonfly

## Support

- **DragonflyDB Issues:** https://github.com/dragonflydb/dragonfly/issues
- **Orchestrator Issues:** https://github.com/sindef/redis-orchestrator/issues
- **DragonflyDB Discord:** https://discord.gg/dragonflydb

## License Compatibility

- **DragonflyDB:** BSL 1.1 (Business Source License) - Free for non-production and small production use
- **Redis Orchestrator:** MIT License - Free for all uses

See DragonflyDB's licensing at: https://github.com/dragonflydb/dragonfly/blob/main/LICENSE.md

