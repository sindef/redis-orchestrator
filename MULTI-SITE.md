# Multi-Site Deployment Guide

## The Problem: Same Pod Names Across Sites

When deploying Redis orchestrator across multiple sites (e.g., two Kubernetes clusters), you may have pods with identical names:

```
Site 1:  redis-0, redis-1, redis-2
Site 2:  redis-0, redis-1, redis-2
```

If both `redis-0` pods start at the same time, how do we deterministically elect one as master?

## The Solution: Three-Tier Tie-Breaking

The orchestrator uses a three-tier election algorithm:

### 1. Startup Time (Primary)
**The oldest pod wins**

```
Site 1: redis-0 (started 10m ago) ← WINS
Site 2: redis-0 (started 5m ago)
```

### 2. Pod Name (Secondary)
**Lexicographically smallest name wins** (for single-site scenarios)

```
redis-0 (started at 12:00) ← WINS
redis-1 (started at 12:00)
redis-2 (started at 12:00)
```

### 3. Pod UID (Tertiary)
**Lexicographically smallest UID wins** (for multi-site with same names)

```
Site 1: redis-0 (UID: zzz-abc, started at 12:00)
Site 2: redis-0 (UID: aaa-xyz, started at 12:00) ← WINS
```

This ensures **deterministic** election even when:
- Multiple sites have identically named pods
- Pods start at the exact same time
- Network partitions create split-brain scenarios

## Example Scenario

### Setup

```yaml
# Site 1 - US East
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis
  namespace: redis-site1
spec:
  replicas: 2
  # ... redis-0, redis-1

# Site 2 - US West  
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis
  namespace: redis-site2
spec:
  replicas: 2
  # ... redis-0, redis-1
```

### Election Process

**Initial State (all pods start at 12:00:00)**

| Pod | UID | Startup Time | Name | Result |
|-----|-----|--------------|------|--------|
| Site1/redis-0 | bbb-111 | 12:00:00 | redis-0 | - |
| Site2/redis-0 | aaa-222 | 12:00:00 | redis-0 | **MASTER** ← (UID wins) |
| Site1/redis-1 | ccc-333 | 12:00:00 | redis-1 | - |
| Site2/redis-1 | ddd-444 | 12:00:00 | redis-1 | - |

**Winner:** Site2/redis-0 (UID `aaa-222` < `bbb-111`)

## Debug Logging

Enable debug mode to see the election decision-making:

```bash
kubectl set env statefulset/redis DEBUG_FLAG="--debug" -n redis
```

### Example Debug Output

```
============================================
Starting state sync cycle pod=redis-0 namespace=redis-site1

Local Redis state pod=redis-0 uid=bbb-111 namespace=redis-site1 isMaster=false isHealthy=true startupTime=2025-12-05T12:00:00Z

Discovered peers count=3
Peer state pod=redis-0 uid=aaa-222 namespace=redis-site2 isMaster=false isHealthy=true startupTime=2025-12-05T12:00:00Z
Peer state pod=redis-1 uid=ccc-333 namespace=redis-site1 isMaster=false isHealthy=true startupTime=2025-12-05T12:00:00Z
Peer state pod=redis-1 uid=ddd-444 namespace=redis-site2 isMaster=false isHealthy=true startupTime=2025-12-05T12:00:00Z

Reconciliation analysis totalHealthyPods=4 masterCount=0

========================================
NO MASTER DETECTED - Starting election
Election candidates count=4
Candidate rank=1 pod=redis-0 uid=bbb-111 namespace=redis-site1 startupTime=2025-12-05T12:00:00Z ip=10.0.1.5
Candidate rank=2 pod=redis-0 uid=aaa-222 namespace=redis-site2 startupTime=2025-12-05T12:00:00Z ip=10.0.2.5
Candidate rank=3 pod=redis-1 uid=ccc-333 namespace=redis-site1 startupTime=2025-12-05T12:00:00Z ip=10.0.1.6
Candidate rank=4 pod=redis-1 uid=ddd-444 namespace=redis-site2 startupTime=2025-12-05T12:00:00Z ip=10.0.2.6

Election result:
ELECTED MASTER pod=redis-0 uid=aaa-222 namespace=redis-site2 startupTime=2025-12-05T12:00:00Z reason=tie-breaker: pod UID (aaa-222 < bbb-111) - multi-site scenario

We are not the elected master electedPod=redis-0 electedUID=aaa-222 ourPod=redis-0 ourUID=bbb-111
Already correctly configured as replica
```

## Viewing Pod UIDs

To see your pod UIDs:

```bash
kubectl get pods -n redis-site1 -o custom-columns=NAME:.metadata.name,UID:.metadata.uid
kubectl get pods -n redis-site2 -o custom-columns=NAME:.metadata.name,UID:.metadata.uid
```

Example output:
```
NAME      UID
redis-0   bbb-111-xxx-yyy-zzz
redis-1   ccc-222-xxx-yyy-zzz
```

## Testing Multi-Site Election

### 1. Deploy to Both Sites

```bash
# Site 1
kubectl apply -f deploy/statefulset.yaml -n redis-site1

# Site 2
kubectl apply -f deploy/statefulset.yaml -n redis-site2
```

### 2. Watch Election with Debug Logs

```bash
# Site 1
kubectl logs -f redis-0 -c orchestrator -n redis-site1

# Site 2 (in another terminal)
kubectl logs -f redis-0 -c orchestrator -n redis-site2
```

### 3. Verify Master Selection

```bash
# Check all pods
kubectl get pods --all-namespaces -l redis-role=master

# Should see exactly ONE pod with the master label
```

## Split-Brain Scenario

### Scenario: Network Partition

1. **Network partition occurs** between sites
2. Both sites may temporarily elect their own master
3. **When partition heals**, orchestrators detect multiple masters
4. **Split-brain resolution** keeps the master with:
   - Oldest startup time, OR
   - Smallest pod name (if same time), OR
   - Smallest UID (if same name and time)

### Debug Output for Split-Brain

```
========================================
SPLIT BRAIN DETECTED - Multiple masters exist!
Split brain masters count=2
Master index=0 pod=redis-0 uid=aaa-222 namespace=redis-site2 startupTime=2025-12-05T12:00:00Z
Master index=1 pod=redis-0 uid=bbb-111 namespace=redis-site1 startupTime=2025-12-05T12:00:00Z

Split brain resolution decision keepPod=redis-0 keepUID=aaa-222 keepNamespace=redis-site2 
reason=tie-breaker: pod UID (aaa-222 < bbb-111) - multi-site scenario

Masters to demote:
  Will demote pod=redis-0 uid=bbb-111 namespace=redis-site1

WE MUST DEMOTE - We are not the chosen master

========================================
DEMOTING TO REPLICA pod=redis-0 uid=bbb-111 namespace=redis-site1 masterService=redis
```

## Best Practices

### 1. Use Unique Namespaces Per Site

```yaml
# Site 1
namespace: redis-site1

# Site 2
namespace: redis-site2
```

This makes troubleshooting easier (you can see which site a pod belongs to).

### 2. Enable Debug Logging During Initial Deployment

```yaml
args:
- --debug
- --redis-host=localhost
- --redis-port=6379
```

Turn off debug once stable to reduce log volume.

### 3. Monitor for Repeated Elections

If you see frequent elections, it indicates:
- Network instability
- Pod crashes
- Clock skew (check NTP)

### 4. Use Service Mesh for Cross-Site Discovery

For proper multi-site operation, you need:
- **Istio**, **Linkerd**, or similar service mesh
- Cross-cluster service discovery
- Exported services between sites

### 5. Test Failover Regularly

```bash
# Delete master in one site
MASTER=$(kubectl get pods --all-namespaces -l redis-role=master -o jsonpath='{.items[0].metadata.name}')
MASTER_NS=$(kubectl get pods --all-namespaces -l redis-role=master -o jsonpath='{.items[0].metadata.namespace}')

kubectl delete pod -n $MASTER_NS $MASTER

# Watch new master election
kubectl get pods --all-namespaces -l redis-role=master -w
```

## Troubleshooting

### Problem: Two Pods Claim to be Master

**Check UIDs:**
```bash
kubectl get pods --all-namespaces -o custom-columns=NAME:.metadata.name,NAMESPACE:.metadata.namespace,UID:.metadata.uid,MASTER:.metadata.labels.redis-role
```

**Check orchestrator logs:**
```bash
kubectl logs redis-0 -c orchestrator -n redis-site1 | grep "ELECTED MASTER"
kubectl logs redis-0 -c orchestrator -n redis-site2 | grep "ELECTED MASTER"
```

**Resolution:** The orchestrator will automatically resolve this in the next sync cycle (default 15s).

### Problem: Different Pod Elected Than Expected

UIDs are randomly generated by Kubernetes. You cannot predict which pod will win when names and startup times are identical. The important thing is that **all orchestrators reach the same decision**.

### Problem: Master Keeps Switching

This indicates clock skew between sites. Check:

```bash
# Check pod startup times
kubectl get pods --all-namespaces -o custom-columns=NAME:.metadata.name,START:.status.startTime

# Check system time on nodes
kubectl get nodes -o wide
```

## Command-Line Reference

```bash
# Enable debug logging
kubectl set env statefulset/redis -c orchestrator DEBUG="--debug"

# View master election history
kubectl logs redis-0 -c orchestrator | grep "ELECTED MASTER"

# View all promotion/demotion events
kubectl logs redis-0 -c orchestrator | grep -E "PROMOTING|DEMOTING"

# Check current cluster state
kubectl get pods --all-namespaces -l app=redis \
  -o custom-columns=NAME:.metadata.name,NAMESPACE:.metadata.namespace,ROLE:.metadata.labels.redis-role,IP:.status.podIP

# Test split-brain detection (requires debug mode)
kubectl logs -f redis-0 -c orchestrator | grep "SPLIT BRAIN"
```

## Conclusion

The three-tier tie-breaking mechanism ensures:
- ✅ **Deterministic elections** even with identical pod names
- ✅ **Multi-site support** without requiring unique naming schemes
- ✅ **Automatic split-brain resolution** during network partitions
- ✅ **Debuggable** with verbose logging showing exact election reasoning

The Pod UID acts as a globally unique, stable identifier that breaks ties when all else is equal.

