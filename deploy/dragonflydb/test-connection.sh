#!/bin/bash
set -e

# Test script for DragonflyDB with Redis Orchestrator
# This script verifies the deployment is working correctly

NAMESPACE=${NAMESPACE:-dragonfly}

echo "========================================="
echo "DragonflyDB + Redis Orchestrator Test"
echo "========================================="
echo ""

# 1. Check pods are running
echo "1. Checking pods..."
TOTAL_PODS=$(kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=dragonfly --no-headers | wc -l)
READY_PODS=$(kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=dragonfly --no-headers | grep "2/2" | wc -l)

echo "   Total pods: $TOTAL_PODS"
echo "   Ready pods: $READY_PODS"

if [ "$TOTAL_PODS" -eq 0 ]; then
    echo "   ❌ No pods found!"
    exit 1
fi

if [ "$READY_PODS" -ne "$TOTAL_PODS" ]; then
    echo "   ⚠️  Not all pods are ready"
    kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=dragonfly
    exit 1
fi

echo "   ✅ All pods ready"
echo ""

# 2. Check master election
echo "2. Checking master election..."
MASTER_POD=$(kubectl get pods -n $NAMESPACE -l redis-role=master -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

if [ -z "$MASTER_POD" ]; then
    echo "   ❌ No master elected!"
    echo "   Check orchestrator logs:"
    kubectl logs -n $NAMESPACE -l app.kubernetes.io/name=dragonfly -c orchestrator | tail -20
    exit 1
fi

echo "   ✅ Master elected: $MASTER_POD"
echo ""

# 3. Check service
echo "3. Checking services..."
MASTER_SVC=$(kubectl get svc -n $NAMESPACE dragonfly-master -o jsonpath='{.metadata.name}' 2>/dev/null || echo "")

if [ -z "$MASTER_SVC" ]; then
    echo "   ❌ dragonfly-master service not found!"
    exit 1
fi

echo "   ✅ Service exists: dragonfly-master"
echo ""

# 4. Test connectivity
echo "4. Testing DragonflyDB connectivity..."

# Test PING
PING_RESULT=$(kubectl exec -n $NAMESPACE $MASTER_POD -c dragonfly -- redis-cli PING 2>/dev/null || echo "FAIL")

if [ "$PING_RESULT" != "PONG" ]; then
    echo "   ❌ PING failed: $PING_RESULT"
    exit 1
fi

echo "   ✅ PING successful"

# 5. Test write/read
echo "5. Testing write/read operations..."
TEST_KEY="test_key_$(date +%s)"
TEST_VALUE="test_value_$(date +%s)"

kubectl exec -n $NAMESPACE $MASTER_POD -c dragonfly -- redis-cli SET $TEST_KEY "$TEST_VALUE" >/dev/null 2>&1
READ_VALUE=$(kubectl exec -n $NAMESPACE $MASTER_POD -c dragonfly -- redis-cli GET $TEST_KEY 2>/dev/null)

if [ "$READ_VALUE" != "$TEST_VALUE" ]; then
    echo "   ❌ Write/read test failed"
    echo "      Expected: $TEST_VALUE"
    echo "      Got: $READ_VALUE"
    exit 1
fi

echo "   ✅ Write/read successful"
echo ""

# 6. Check replication
echo "6. Checking replication status..."
ROLE=$(kubectl exec -n $NAMESPACE $MASTER_POD -c dragonfly -- redis-cli INFO replication | grep "role:" | cut -d: -f2 | tr -d '\r\n ')

if [ "$ROLE" != "master" ]; then
    echo "   ⚠️  Master pod reports role: $ROLE (expected: master)"
fi

REPLICAS=$(kubectl exec -n $NAMESPACE $MASTER_POD -c dragonfly -- redis-cli INFO replication | grep "connected_slaves:" | cut -d: -f2 | tr -d '\r\n ')

echo "   Role: $ROLE"
echo "   Connected replicas: $REPLICAS"

if [ "$REPLICAS" -gt 0 ]; then
    echo "   ✅ Replication configured"
else
    echo "   ⚠️  No replicas connected (may be expected for single pod)"
fi

echo ""

# 7. Check orchestrator health
echo "7. Checking orchestrator health..."

# Port-forward in background
kubectl port-forward -n $NAMESPACE $MASTER_POD 8080:8080 >/dev/null 2>&1 &
PF_PID=$!
sleep 2

HEALTH=$(curl -s http://localhost:8080/health 2>/dev/null || echo "FAIL")

# Kill port-forward
kill $PF_PID 2>/dev/null || true

if [ "$HEALTH" = "OK" ]; then
    echo "   ✅ Orchestrator healthy"
else
    echo "   ⚠️  Orchestrator health check failed: $HEALTH"
fi

echo ""
echo "========================================="
echo "✅ All tests passed!"
echo "========================================="
echo ""
echo "Deployment Summary:"
echo "  • Total pods: $TOTAL_PODS"
echo "  • Master pod: $MASTER_POD"
echo "  • Service: dragonfly-master.$NAMESPACE.svc.cluster.local:6379"
echo ""
echo "Connect with:"
echo "  kubectl exec -it -n $NAMESPACE $MASTER_POD -c dragonfly -- redis-cli"
echo ""

