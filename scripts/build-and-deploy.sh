#!/bin/bash
set -e

# Build and deploy script for Redis Orchestrator

VERSION=${VERSION:-latest}
REGISTRY=${REGISTRY:-docker.io/yourname}
NAMESPACE=${NAMESPACE:-default}

echo "Building Redis Orchestrator version: $VERSION"

# Build Go binary
echo "=> Building Go binary..."
CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o redis-orchestrator .

# Build Docker image
echo "=> Building Docker image..."
docker build -t ${REGISTRY}/redis-orchestrator:${VERSION} .

# Push to registry (optional)
if [ "$PUSH" = "true" ]; then
    echo "=> Pushing to registry..."
    docker push ${REGISTRY}/redis-orchestrator:${VERSION}
fi

# Deploy to Kubernetes
if [ "$DEPLOY" = "true" ]; then
    echo "=> Deploying to Kubernetes namespace: $NAMESPACE"
    
    # Create namespace if it doesn't exist
    kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -
    
    # Apply manifests
    kubectl apply -f deploy/statefulset.yaml -n $NAMESPACE
    
    echo "=> Waiting for rollout..."
    kubectl rollout status statefulset/redis -n $NAMESPACE --timeout=5m
    
    echo "=> Deployment complete!"
    kubectl get pods -l app=redis -n $NAMESPACE
fi

echo "Done!"

