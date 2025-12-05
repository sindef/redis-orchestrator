# Redis Orchestrator

A Kubernetes-native orchestrator for Redis master-replica failover and leader election. Redis Orchestrator automatically manages Redis master election, handles failover scenarios, and maintains cluster consistency in Kubernetes environments.

## Features

- **Automatic Master Election**: Elects a Redis master from available healthy pods using configurable strategies
- **Failover Handling**: Automatically promotes a new master when the current master fails
- **Split-Brain Resolution**: Detects and resolves multiple master scenarios
- **Dual Election Modes**:
  - **Deterministic**: Simple timestamp and pod-name based election (default)
  - **Raft Consensus**: Production-grade consensus-based leader election using Hashicorp Raft
- **Kubernetes Integration**: Native integration with Kubernetes API for pod discovery and labeling
- **Witness Mode**: Support for Raft witness nodes (non-voting members) for odd-numbered clusters
- **Peer Authentication**: Optional shared secret authentication for secure peer communication
- **TLS Support**: Full TLS support for Redis connections
- **Health Monitoring**: Continuous health checks and state synchronization
- **HTTP API**: RESTful API for state queries and cluster management

## Architecture

Redis Orchestrator runs as a sidecar container alongside each Redis instance in your Kubernetes cluster. It:

1. **Monitors** local Redis state and health
2. **Discovers** other orchestrator instances via Kubernetes API
3. **Elects** a master using the configured election strategy
4. **Reconciles** Redis configuration to match the elected master
5. **Labels** Kubernetes pods to indicate master status
6. **Handles** failover when the master becomes unavailable

### Components

- **Orchestrator**: Main coordination logic and state management
- **Election Strategy**: Pluggable election algorithms (Deterministic or Raft)
- **Redis Client**: Redis connection and replication management
- **Kubernetes Client**: Pod discovery and labeling
- **HTTP Server**: Peer communication and status API

## Installation

### Prerequisites

- Kubernetes cluster (1.21+)
- Redis instances running in Kubernetes
- RBAC permissions for pod listing and updates
- Go 1.21+ (for building from source)

### Quick Start

1. **Clone the repository**:
   ```bash
   git clone https://github.com/sindef/redis-orchestrator.git
   cd redis-orchestrator
   ```

2. **Build the Docker image**:
   ```bash
   docker build -t redis-orchestrator:latest .
   ```

3. **Deploy to Kubernetes**:
   ```bash
   kubectl apply -f deploy/statefulset.yaml
   ```

### Using Pre-built Images

```bash
docker pull <registry>/redis-orchestrator:latest
```

## Configuration

Redis Orchestrator is configured via command-line flags and environment variables.

### Required Configuration

- `--pod-name`: Pod name (typically from `POD_NAME` env var via downward API)
- `--namespace`: Kubernetes namespace (typically from `POD_NAMESPACE` env var)
- `--redis-host`: Redis hostname (default: `localhost`)
- `--redis-port`: Redis port (default: `6379`)

### Redis Configuration

```bash
--redis-host=localhost          # Redis host
--redis-port=6379               # Redis port
--redis-password=secret         # Redis password (or REDIS_PASSWORD env)
--redis-tls                     # Enable TLS for Redis
--redis-tls-skip-verify         # Skip TLS certificate verification
--redis-service=redis           # Service name for replica configuration
```

### Kubernetes Configuration

```bash
--pod-name=redis-0              # Pod name (from POD_NAME env)
--namespace=default             # Namespace (from POD_NAMESPACE env)
--label-selector=app=redis      # Label selector for pod discovery
```

### Election Mode Configuration

#### Deterministic Mode (Default)

Simple election based on startup time and pod name:

```bash
--election-mode=deterministic
```

#### Raft Mode

Production-grade consensus-based election:

```bash
--election-mode=raft
--raft-bind=0.0.0.0:7000        # Raft bind address
--raft-peers=pod-0:7000,pod-1:7000,pod-2:7000  # Peer addresses
--raft-data-dir=/var/lib/redis-orchestrator/raft  # Raft data directory
--raft-bootstrap                 # Bootstrap cluster (auto-detected for pod-0)
```

**Raft Auto-Discovery**: If `--raft-peers` is provided, nodes will automatically discover and join the cluster. The first pod (ending with `-0`) will automatically bootstrap if no existing state is found.

### Authentication

```bash
--shared-secret=my-secret       # Shared secret for peer auth (or SHARED_SECRET env)
```

### Standalone/Witness Mode

Run as a Raft witness node (non-voting, doesn't manage Redis):

```bash
--standalone                     # Enable witness mode
--election-mode=raft            # Required for standalone mode
```

### Other Options

```bash
--sync-interval=15s             # State sync interval (default: 15s)
--debug                         # Enable debug logging
```

## Deployment Examples

### Basic StatefulSet Deployment

See `deploy/statefulset.yaml` for a complete example. Key components:

```yaml
containers:
  - name: redis
    image: redis:7-alpine
    # ... Redis configuration ...
  
  - name: orchestrator
    image: redis-orchestrator:latest
    env:
      - name: POD_NAME
        valueFrom:
          fieldRef:
            fieldPath: metadata.name
      - name: POD_NAMESPACE
        valueFrom:
          fieldRef:
            fieldPath: metadata.namespace
    args:
      - --redis-host=localhost
      - --redis-port=6379
      - --redis-password=$(REDIS_PASSWORD)
      - --election-mode=deterministic
```

### Raft Mode Deployment

For production deployments with Raft consensus:

```yaml
args:
  - --election-mode=raft
  - --raft-bind=0.0.0.0:7000
  - --raft-peers=redis-0.redis:7000,redis-1.redis:7000,redis-2.redis:7000
  - --raft-data-dir=/var/lib/redis-orchestrator/raft
  - --shared-secret=$(SHARED_SECRET)
```

See `deploy/raft/` for complete Raft deployment examples.

### Witness Node Deployment

Add witness nodes for odd-numbered Raft clusters:

```yaml
args:
  - --election-mode=raft
  - --standalone
  - --raft-bind=0.0.0.0:7000
  - --raft-peers=redis-0.redis:7000,redis-1.redis:7000,redis-2.redis:7000
```

See `deploy/raft/witness-deployment.yaml` for a complete example.

## Election Modes

### Deterministic Mode

**Use Case**: Simple deployments, development environments, single-site clusters

**How it works**:
1. All healthy pods participate in election
2. Pods are sorted by: startup time (oldest first) → pod name → pod UID
3. The first pod in the sorted list becomes master
4. Election happens on every sync cycle

**Pros**:
- Simple and predictable
- No additional dependencies
- Fast election

**Cons**:
- No consensus guarantees
- Potential for split-brain in network partitions
- Less suitable for multi-site deployments

### Raft Mode

**Use Case**: Production deployments, multi-site clusters, high availability requirements

**How it works**:
1. Uses Hashicorp Raft for consensus
2. Raft leader becomes Redis master
3. Automatic leader election and failover
4. Strong consistency guarantees

**Pros**:
- Strong consensus guarantees
- Handles network partitions correctly
- Production-grade reliability
- Supports witness nodes for odd-numbered clusters

**Cons**:
- More complex setup
- Requires persistent storage for Raft state
- Slightly higher resource usage

## API Endpoints

Redis Orchestrator exposes an HTTP API on port `8080`:

### Health Check

```bash
GET /health
```

Returns `200 OK` if the orchestrator is running.

### State Endpoint

```bash
GET /state
```

Returns the current pod state (requires authentication if `--shared-secret` is set):

```json
{
  "podName": "redis-0",
  "podIP": "10.244.1.5",
  "podUID": "abc123...",
  "namespace": "default",
  "isMaster": true,
  "isHealthy": true,
  "isWitness": false,
  "startupTime": "2024-01-01T12:00:00Z",
  "lastSeen": "2024-01-01T12:05:00Z"
}
```

### Raft Endpoints (Raft Mode Only)

```bash
GET /raft/status          # Raft cluster status
GET /raft/peers           # List of Raft peers
POST /raft/add-voter      # Add a voting member
POST /raft/add-nonvoter   # Add a non-voting member (witness)
```

All Raft endpoints require authentication if `--shared-secret` is set.

## Testing

### Failover Test

Use the provided test script to simulate master failover:

```bash
./scripts/test-failover.sh
```

This script:
1. Identifies the current master
2. Writes test data
3. Deletes the master pod
4. Verifies a new master is elected
5. Checks data replication

### Manual Testing

1. **Check current master**:
   ```bash
   kubectl get pods -l redis-role=master
   ```

2. **View orchestrator logs**:
   ```bash
   kubectl logs <pod-name> -c orchestrator
   ```

3. **Query orchestrator state**:
   ```bash
   kubectl port-forward <pod-name> 8080:8080
   curl http://localhost:8080/state
   ```

4. **Simulate failover**:
   ```bash
   kubectl delete pod <master-pod-name>
   ```

## Troubleshooting

### No Master Elected

- Check that pods are running and healthy
- Verify Redis is accessible from orchestrator
- Check orchestrator logs for errors
- Ensure `POD_NAME` and `POD_NAMESPACE` are set correctly

### Split-Brain Detected

- Check network connectivity between pods
- Verify all pods can reach each other
- Review orchestrator logs for reconciliation attempts
- In Raft mode, check Raft cluster health

### Raft Cluster Not Forming

- Verify `--raft-peers` includes all pod addresses
- Check that pod-0 has `--raft-bootstrap` or auto-bootstrap is working
- Ensure Raft data directory is writable and persistent
- Review Raft logs for join errors
- Check network connectivity on Raft port (default: 7000)

### Authentication Errors

- Verify `--shared-secret` is set consistently across all pods
- Check that `SHARED_SECRET` environment variable is set if using env var
- Review HTTP request logs for authentication failures

### Debug Mode

Enable debug logging for detailed information:

```bash
--debug
```

This provides:
- Detailed election logs
- State synchronization details
- Raft consensus information
- HTTP request/response logs

## Building from Source

```bash
# Clone repository
git clone https://github.com/sindef/redis-orchestrator.git
cd redis-orchestrator

# Build binary
go build -o redis-orchestrator .

# Build Docker image
docker build -t redis-orchestrator:latest .
```

## Development

### Running Tests

```bash
go test ./...
```

### Project Structure

```
redis-orchestrator/
├── main.go                    # Entry point
├── pkg/
│   ├── config/               # Configuration types
│   ├── orchestrator/         # Main orchestration logic
│   │   └── state/           # State management
│   ├── election/             # Election strategies
│   │   ├── deterministic.go  # Deterministic election
│   │   ├── raft.go          # Raft consensus
│   │   └── raft_discovery.go # Raft cluster discovery
│   ├── redis/                # Redis client wrapper
│   └── auth/                 # Peer authentication
├── deploy/                   # Kubernetes manifests
│   ├── statefulset.yaml     # Basic deployment
│   └── raft/                # Raft deployment examples
├── scripts/                  # Utility scripts
└── Dockerfile               # Container build
```

## License

[Add your license here]

## Contributing

[Add contribution guidelines here]

## Support

For issues and questions:
- GitHub Issues: [Add link]
- Documentation: [Add link]

