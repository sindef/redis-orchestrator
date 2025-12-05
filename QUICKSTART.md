# Quick Start Guide

Get Redis Orchestrator running in your Kubernetes cluster in 5 minutes.

## Prerequisites

- Kubernetes cluster (1.20+)
- `kubectl` configured
- Docker (for building image)

## Step 1: Clone and Build

```bash
cd /home/mnorris/repos/redis-orchestrator

# Download dependencies
go mod download
go mod tidy

# Build Docker image
docker build -t redis-orchestrator:v1.0.0 .

# Tag for your registry (optional)
docker tag redis-orchestrator:v1.0.0 YOUR_REGISTRY/redis-orchestrator:v1.0.0
docker push YOUR_REGISTRY/redis-orchestrator:v1.0.0
```

## Step 2: Update Deployment Manifest

Edit `deploy/statefulset.yaml` and update the image:

```yaml
containers:
- name: orchestrator
  image: YOUR_REGISTRY/redis-orchestrator:v1.0.0  # Update this line
```

## Step 3: Deploy to Kubernetes

```bash
# Create namespace (optional)
kubectl create namespace redis

# Deploy
kubectl apply -f deploy/statefulset.yaml -n redis

# Watch pods come up
kubectl get pods -n redis -w
```

## Step 4: Verify Installation

```bash
# Check pods
kubectl get pods -n redis -l app=redis

# Should see output like:
# NAME      READY   STATUS    RESTARTS   AGE
# redis-0   2/2     Running   0          2m
# redis-1   2/2     Running   0          2m
# redis-2   2/2     Running   0          2m

# Check which pod is master
kubectl get pods -n redis -l redis-role=master

# Should see one pod with the master label
```

## Step 5: Test Redis Connection

```bash
# Get Redis password
REDIS_PASSWORD=$(kubectl get secret redis-secret -n redis -o jsonpath='{.data.password}' | base64 -d)

# Test connection via service
kubectl run -it --rm redis-cli --image=redis:7-alpine --restart=Never -n redis -- \
  redis-cli -h redis -a $REDIS_PASSWORD PING

# Should output: PONG
```

## Step 6: Test Failover

```bash
# Run the failover test script
cd /home/mnorris/repos/redis-orchestrator
NAMESPACE=redis ./scripts/test-failover.sh

# This will:
# 1. Identify current master
# 2. Write test data
# 3. Delete master pod
# 4. Wait for new master election
# 5. Verify failover succeeded
```

## Common Issues

### Pods stuck in "Pending"

Check PVC creation:
```bash
kubectl get pvc -n redis
```

If storage class not available, edit `deploy/statefulset.yaml`:
```yaml
volumeClaimTemplates:
- metadata:
    name: data
  spec:
    storageClassName: standard  # Add this line with your storage class
```

### Orchestrator logs show permission errors

Verify RBAC:
```bash
kubectl auth can-i update pods \
  --as=system:serviceaccount:redis:redis-orchestrator \
  -n redis

# Should output: yes
```

### No master elected

Check orchestrator logs:
```bash
kubectl logs -n redis redis-0 -c orchestrator

# Look for election messages
```

### Redis connection refused

Check Redis is running:
```bash
kubectl logs -n redis redis-0 -c redis
```

Check password:
```bash
kubectl get secret redis-secret -n redis -o yaml
```

## What's Next?

- **Production configuration**: See `README.md` for production best practices
- **TLS setup**: See `deploy/tls-example.yaml`
- **Multi-site**: See `deploy/multi-site-example.yaml`
- **Monitoring**: Add Prometheus metrics (coming soon)

## Quick Commands Reference

```bash
# View orchestrator logs
kubectl logs -f redis-0 -c orchestrator -n redis

# View Redis logs
kubectl logs -f redis-0 -c redis -n redis

# Check current master
kubectl get pods -n redis -l redis-role=master

# Manually trigger failover (delete master)
MASTER=$(kubectl get pods -n redis -l redis-role=master -o jsonpath='{.items[0].metadata.name}')
kubectl delete pod -n redis $MASTER

# Check replication status
kubectl exec -n redis redis-0 -c redis -- redis-cli -a $REDIS_PASSWORD INFO replication

# Access Redis CLI
kubectl exec -it redis-0 -c redis -n redis -- redis-cli -a $REDIS_PASSWORD

# Delete everything
kubectl delete -f deploy/statefulset.yaml -n redis
kubectl delete pvc -n redis -l app=redis
```

## Success Criteria

You should have:
- ✅ 3 Redis pods running (2/2 containers each)
- ✅ 1 pod labeled with `redis-role=master`
- ✅ Service `redis` resolving to master pod
- ✅ Successful Redis PING via service
- ✅ Successful failover test (master switches on pod deletion)

If all checks pass, you're ready for production (after security hardening)!

## Getting Help

- Check logs: `kubectl logs POD_NAME -c orchestrator -n redis`
- Review design: See `DESIGN.md`
- Full documentation: See `README.md`

