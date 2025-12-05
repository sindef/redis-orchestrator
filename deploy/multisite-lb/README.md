# Multi-Site Redis Deployment with LoadBalancer Services

This directory contains Kubernetes manifests for deploying Redis with redis-orchestrator in a multi-site configuration using Raft consensus, where pods communicate across clusters via LoadBalancer services and external DNS names.

## Architecture

- **Site A** (au-southeast-2): 2 Redis data nodes (redis-0, redis-1)
- **Site B** (au-southeast-1): 2 Redis data nodes (redis-0, redis-1)
- **Site C** (au-southeast-3): 1 Witness node (tie-breaker for Raft quorum)

Each pod has its own LoadBalancer service for cross-cluster Raft communication. All Redis connections use TLS encryption with certificates managed by cert-manager.

## Deployment Structure

```
multisite-lb/
├── site-a-manifests/
│   ├── namespace.yaml
│   ├── rbac.yaml
│   ├── secret.yaml
│   ├── configmap.yaml
│   ├── cert-manager.yaml              # cert-manager issuer and certificate
│   ├── statefulset.yaml
│   ├── services.yaml
│   ├── pod-loadbalancer-service.yaml  # Individual LB for redis-0
│   ├── pod-loadbalancer-service-redis-1.yaml  # Individual LB for redis-1
│   └── loadbalancer-services.yaml     # Master/Readonly LB services
├── site-b-manifests/
│   ├── namespace.yaml
│   ├── rbac.yaml
│   ├── secret.yaml
│   ├── configmap.yaml
│   ├── cert-manager.yaml              # cert-manager issuer and certificate
│   ├── statefulset.yaml
│   ├── services.yaml
│   ├── pod-loadbalancer-service.yaml  # Individual LB for redis-0
│   ├── pod-loadbalancer-service-redis-1.yaml  # Individual LB for redis-1
│   └── loadbalancer-services.yaml     # Master/Readonly LB services
└── site-c-manifests/
    ├── namespace.yaml
    ├── rbac.yaml
    ├── secret.yaml
    ├── witness-deployment.yaml
    ├── services.yaml
    └── pod-loadbalancer-service.yaml  # Individual LB for witness
```

## Prerequisites

1. Kubernetes clusters in each site with support for LoadBalancer services
2. Storage classes configured for persistent volumes
3. DNS configuration to point external hostnames to LoadBalancer IPs
4. Network connectivity between LoadBalancer IPs across sites
5. **cert-manager installed** in each cluster for TLS certificate management

## Critical Configuration

### External DNS Names

Each pod must be accessible via external DNS names for Raft communication:

- **Site A**: 
  - `redis-0.au-southeast-2.example.com:7000`
  - `redis-1.au-southeast-2.example.com:7000`
- **Site B**: 
  - `redis-0.au-southeast-1.example.com:7000`
  - `redis-1.au-southeast-1.example.com:7000`
- **Site C**: `redis-witness.au-southeast-3.example.com:7000`

**IMPORTANT**: After deploying the LoadBalancer services, you must:

1. Get the external IPs from each LoadBalancer service:
   ```bash
   kubectl get svc redis-0-lb -n redis-multisite-a
   kubectl get svc redis-1-lb -n redis-multisite-a
   kubectl get svc redis-0-lb -n redis-multisite-b
   kubectl get svc redis-1-lb -n redis-multisite-b
   kubectl get svc redis-witness-lb -n redis-multisite-c
   ```

2. Configure DNS records to point to these IPs:
   - `redis-0.au-southeast-2.example.com` → Site A redis-0 LoadBalancer IP
   - `redis-1.au-southeast-2.example.com` → Site A redis-1 LoadBalancer IP
   - `redis-0.au-southeast-1.example.com` → Site B redis-0 LoadBalancer IP
   - `redis-1.au-southeast-1.example.com` → Site B redis-1 LoadBalancer IP
   - `redis-witness.au-southeast-3.example.com` → Site C LoadBalancer IP

3. If using external-dns controller, uncomment the annotation in `pod-loadbalancer-service.yaml` files.

### Shared Secrets

**CRITICAL**: The `shared-secret` and `redis-password` in the secret files must be **identical across all sites**.

Before deploying, update the secrets in:
- `site-a-manifests/secret.yaml`
- `site-b-manifests/secret.yaml`
- `site-c-manifests/secret.yaml`

Generate a strong secret:
```bash
openssl rand -base64 32
```

### Raft Peer Configuration

All Raft peers are configured to use external DNS names (not Kubernetes internal DNS):
- `redis-0.au-southeast-2.example.com:7000` (Site A, pod 0)
- `redis-1.au-southeast-2.example.com:7000` (Site A, pod 1)
- `redis-0.au-southeast-1.example.com:7000` (Site B, pod 0)
- `redis-1.au-southeast-1.example.com:7000` (Site B, pod 1)
- `redis-witness.au-southeast-3.example.com:7000` (Site C, witness)

These DNS names must resolve to the LoadBalancer IPs before the Raft cluster can form.

### TLS Configuration

All Redis connections use TLS encryption:
- **Redis TLS Port**: 6380 (instead of standard 6379)
- **Certificates**: Managed by cert-manager with self-signed issuer
- **Certificate Secret**: `redis-tls` (created by cert-manager Certificate resource)
- **TLS Protocols**: TLSv1.2 and TLSv1.3

The cert-manager Certificate resources include all necessary DNS names for service discovery and external access.

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

4. **Get LoadBalancer IPs and configure DNS**:
   ```bash
   # Get IPs
   kubectl get svc redis-0-lb -n redis-multisite-a -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
   kubectl get svc redis-0-lb -n redis-multisite-b -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
   kubectl get svc redis-witness-lb -n redis-multisite-c -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
   
   # Configure DNS records pointing to these IPs
   ```

5. **Verify DNS resolution** (from within pods):
   ```bash
   kubectl exec -it redis-0 -n redis-multisite-a -- nslookup redis-0.au-southeast-2.example.com
   kubectl exec -it redis-0 -n redis-multisite-a -- nslookup redis-1.au-southeast-2.example.com
   kubectl exec -it redis-0 -n redis-multisite-a -- nslookup redis-0.au-southeast-1.example.com
   kubectl exec -it redis-0 -n redis-multisite-a -- nslookup redis-1.au-southeast-1.example.com
   kubectl exec -it redis-0 -n redis-multisite-a -- nslookup redis-witness.au-southeast-3.example.com
   ```

6. **Verify TLS certificates are issued**:
   ```bash
   kubectl get certificate -n redis-multisite-a
   kubectl get certificate -n redis-multisite-b
   kubectl get secret redis-tls -n redis-multisite-a
   ```

## Services

### ClusterIP Services (Internal)

Each site has:
- `redis`: Routes to the current master
- `redis-readonly`: Routes to all replicas for read-only access
- `redis-headless`: Headless service for pod discovery

### LoadBalancer Services

#### Individual Pod LoadBalancers (for Raft communication)
- **Site A**: 
  - `redis-0-lb` - Exposes redis-0 pod (ports: 7000, 8080, 6380)
  - `redis-1-lb` - Exposes redis-1 pod (ports: 7000, 8080, 6380)
- **Site B**: 
  - `redis-0-lb` - Exposes redis-0 pod (ports: 7000, 8080, 6380)
  - `redis-1-lb` - Exposes redis-1 pod (ports: 7000, 8080, 6380)
- **Site C**: `redis-witness-lb` - Exposes witness pod (ports: 7000, 8080)

#### Application LoadBalancers (for client access)
- **Site A**: 
  - `redis-master-lb`: External access to master (TLS port 6380)
  - `redis-readonly-lb`: External read-only access (TLS port 6380)
- **Site B**: 
  - `redis-master-lb`: External access to master (TLS port 6380)
  - `redis-readonly-lb`: External read-only access (TLS port 6380)

**Note**: All Redis connections require TLS. Use `redis-cli --tls --cert <cert> --key <key> --cacert <ca> -p 6380` for connections.

## Accessing Redis

### Internal Access

```bash
# Connect to master (from within cluster) - requires TLS
kubectl run -it --rm redis-client --image=redis:7-alpine --restart=Never -- \
  sh -c 'apk add --no-cache openssl && \
  redis-cli --tls --cert /tls/tls.crt --key /tls/tls.key --cacert /tls/ca.crt \
  -h redis.redis-multisite-a.svc.cluster.local -p 6380 -a <password>'

# Or copy certificates to a pod first, then connect
kubectl cp redis-multisite-a/redis-0:/tls ./tls-certs
kubectl run -it --rm redis-client --image=redis:7-alpine --restart=Never \
  --mount type=bind,source=$(pwd)/tls-certs,target=/tls -- \
  redis-cli --tls --cert /tls/tls.crt --key /tls/tls.key --cacert /tls/ca.crt \
  -h redis.redis-multisite-a.svc.cluster.local -p 6380 -a <password>
```

### External Access

After LoadBalancer services are provisioned:

```bash
# Get LoadBalancer IP for master
kubectl get svc redis-master-lb -n redis-multisite-a

# Get TLS certificates (extract from secret)
kubectl get secret redis-tls -n redis-multisite-a -o jsonpath='{.data.tls\.crt}' | base64 -d > tls.crt
kubectl get secret redis-tls -n redis-multisite-a -o jsonpath='{.data.tls\.key}' | base64 -d > tls.key
kubectl get secret redis-tls -n redis-multisite-a -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt

# Connect externally with TLS
redis-cli --tls --cert tls.crt --key tls.key --cacert ca.crt -h <EXTERNAL-IP> -p 6380 -a <password>
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

1. **Raft peers can't connect**:
   - Verify DNS resolution: `nslookup redis-0.au-southeast-2.example.com`
   - Check LoadBalancer IPs are assigned: `kubectl get svc -A | grep -E "redis-0-lb|redis-witness-lb"`
   - Verify DNS records point to correct LoadBalancer IPs
   - Check firewall rules allow traffic on port 7000 between LoadBalancer IPs

2. **Secrets mismatch**: Ensure all secrets are identical across sites

3. **No master elected**: 
   - Check orchestrator logs: `kubectl logs -n redis-multisite-a redis-0 -c orchestrator`
   - Verify Raft peer connectivity and DNS resolution

4. **LoadBalancer not provisioning**: 
   - Verify your cluster supports LoadBalancer services
   - Check cloud provider LoadBalancer quotas/limits
   - For on-premise, consider using MetalLB or similar

5. **DNS not resolving**:
   - Verify DNS records are created and propagated
   - Check TTL values (use low TTL during setup)
   - Test DNS resolution from within pods

6. **TLS certificate issues**:
   - Verify cert-manager is installed: `kubectl get pods -n cert-manager`
   - Check certificate status: `kubectl get certificate -n redis-multisite-a`
   - Check certificate events: `kubectl describe certificate redis-tls -n redis-multisite-a`
   - Verify secret exists: `kubectl get secret redis-tls -n redis-multisite-a`

7. **Redis TLS connection failures**:
   - Verify Redis is listening on TLS port: `kubectl exec redis-0 -n redis-multisite-a -- netstat -tlnp | grep 6380`
   - Check Redis logs for TLS errors: `kubectl logs redis-0 -n redis-multisite-a -c redis`
   - Verify certificate paths in Redis config

## Network Requirements

For cross-cluster Raft communication, ensure:

1. **Port 7000** (Raft) is open between all LoadBalancer IPs
2. **Port 8080** (Orchestrator HTTP) is accessible for health checks (optional)
3. **Port 6380** (Redis TLS) is accessible for replication and client connections

## Using External-DNS

If you're using external-dns controller, uncomment the annotation in `pod-loadbalancer-service.yaml`:

```yaml
annotations:
  external-dns.alpha.kubernetes.io/hostname: redis-0.au-southeast-2.example.com
```

This will automatically create DNS records pointing to the LoadBalancer IP.

## Cleanup

To remove all resources:

```bash
kubectl delete namespace redis-multisite-a
kubectl delete namespace redis-multisite-b
kubectl delete namespace redis-multisite-c
```

**Note**: Remember to clean up DNS records and LoadBalancer resources in your cloud provider.

