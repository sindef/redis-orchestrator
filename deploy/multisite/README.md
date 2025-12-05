# Multi-Site Redis Deployment

This directory contains Kubernetes manifests for deploying Redis with redis-orchestrator in a multi-site configuration using Raft consensus.

## Architecture

- **Site A**: 1 Redis data node
- **Site B**: 1 Redis data node
- **Site C**: 1 Witness node (tie-breaker for Raft quorum)

## Deployment Structure

```
multisite/
├── site-a-manifests/
│   ├── namespace.yaml
│   ├── rbac.yaml
│   ├── secret.yaml
│   ├── configmap.yaml
│   ├── statefulset.yaml
│   ├── services.yaml
│   └── loadbalancer-services.yaml
├── site-b-manifests/
│   ├── namespace.yaml
│   ├── rbac.yaml
│   ├── secret.yaml
│   ├── configmap.yaml
│   ├── statefulset.yaml
│   ├── services.yaml
│   └── loadbalancer-services.yaml
├── site-c-manifests/
│   ├── namespace.yaml
│   ├── rbac.yaml
│   ├── secret.yaml
│   ├── witness-deployment.yaml
│   └── services.yaml
└── global-loadbalancer-services.yaml
```

## Prerequisites

1. Kubernetes cluster with support for LoadBalancer services
2. Storage classes configured for persistent volumes
3. Network connectivity between all sites (if deploying across clusters)

## Important Configuration Notes

### Shared Secrets

**CRITICAL**: The `shared-secret` and `redis-password` in the secret files must be **identical across all sites** for the Raft cluster to function properly.

Before deploying, update the secrets in:
- `site-a-manifests/secret.yaml`
- `site-b-manifests/secret.yaml`
- `site-c-manifests/secret.yaml`

Generate a strong secret:
```bash
openssl rand -base64 32
```

### Raft Peer Configuration

All Raft peers are configured to communicate across sites using Kubernetes service DNS:
- `redis-0.redis-headless.redis-multisite-a.svc.cluster.local:7000`
- `redis-0.redis-headless.redis-multisite-b.svc.cluster.local:7000`
- `redis-witness.redis-headless.redis-multisite-c.svc.cluster.local:7000`

If deploying across different Kubernetes clusters, you'll need to configure external DNS or use external IPs/endpoints.

## Deployment Order

1. **Deploy site-a** (first data node):
   ```bash
   kubectl apply -f site-a-manifests/
   ```

2. **Deploy site-b** (second data node):
   ```bash
   kubectl apply -f site-b-manifests/
   ```

3. **Deploy site-c** (witness node):
   ```bash
   kubectl apply -f site-c-manifests/
   ```

4. **Deploy global LoadBalancer services** (optional):
   ```bash
   kubectl apply -f global-loadbalancer-services.yaml
   ```

## Services

### ClusterIP Services (Internal)

Each site has:
- `redis`: Routes to the current master
- `redis-readonly`: Routes to all replicas for read-only access
- `redis-headless`: Headless service for pod discovery and Raft communication

### LoadBalancer Services (External)

Each site has LoadBalancer services:
- `redis-master-lb`: External access to master
- `redis-readonly-lb`: External read-only access

Global LoadBalancer services are available in the `redis-multisite-a` namespace:
- `redis-master-global-lb`: Routes to master (currently site-a only)
- `redis-readonly-global-lb`: Routes to replicas (currently site-a only)

**Note**: For true cross-namespace LoadBalancer routing, consider using:
- Ingress controller with external-dns
- ServiceMesh (Istio, Linkerd, etc.)
- Custom Endpoints/EndpointSlices configuration

## Accessing Redis

### Internal Access

```bash
# Connect to master (from within cluster)
kubectl run -it --rm redis-client --image=redis:7-alpine --restart=Never -- \
  redis-cli -h redis.redis-multisite-a.svc.cluster.local -p 6379 -a <password>

# Connect to readonly endpoint
kubectl run -it --rm redis-client --image=redis:7-alpine --restart=Never -- \
  redis-cli -h redis-readonly.redis-multisite-a.svc.cluster.local -p 6379 -a <password>
```

### External Access

After LoadBalancer services are provisioned, get the external IP:

```bash
# Get LoadBalancer IP for master
kubectl get svc redis-master-lb -n redis-multisite-a

# Connect externally
redis-cli -h <EXTERNAL-IP> -p 6379 -a <password>
```

## Monitoring

Check orchestrator health:
```bash
# Site A
kubectl port-forward -n redis-multisite-a svc/redis-headless 8080:8080
curl http://localhost:8080/health

# Site B
kubectl port-forward -n redis-multisite-b svc/redis-headless 8080:8080
curl http://localhost:8080/health

# Site C (Witness)
kubectl port-forward -n redis-multisite-c svc/redis-headless 8080:8080
curl http://localhost:8080/health
```

## Troubleshooting

1. **Raft peers can't connect**: Verify network connectivity and DNS resolution between sites
2. **Secrets mismatch**: Ensure all secrets are identical across sites
3. **No master elected**: Check orchestrator logs and Raft peer connectivity
4. **LoadBalancer not provisioning**: Verify your cluster supports LoadBalancer services (or use NodePort/Ingress)

## Cleanup

To remove all resources:

```bash
kubectl delete namespace redis-multisite-a
kubectl delete namespace redis-multisite-b
kubectl delete namespace redis-multisite-c
```

