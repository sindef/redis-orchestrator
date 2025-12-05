#!/bin/bash
set -e

# Test script to simulate master failover

NAMESPACE=${NAMESPACE:-default}
PASSWORD=${REDIS_PASSWORD:-changeme123}

echo "Redis Failover Test Script"
echo "============================"
echo ""

# Function to get current master
get_master() {
    kubectl get pods -n $NAMESPACE -l app=redis,redis-role=master -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "none"
}

# Function to check Redis role
check_role() {
    local pod=$1
    kubectl exec -n $NAMESPACE $pod -c redis -- redis-cli -a $PASSWORD INFO replication | grep "role:" | cut -d: -f2 | tr -d '\r'
}

echo "1. Finding current master..."
CURRENT_MASTER=$(get_master)

if [ "$CURRENT_MASTER" = "none" ]; then
    echo "   No master found! Waiting for election..."
    sleep 20
    CURRENT_MASTER=$(get_master)
fi

echo "   Current master: $CURRENT_MASTER"
echo ""

echo "2. Verifying master role in Redis..."
ROLE=$(check_role $CURRENT_MASTER)
echo "   Redis reports role: $ROLE"
echo ""

echo "3. Writing test data to master..."
kubectl exec -n $NAMESPACE $CURRENT_MASTER -c redis -- redis-cli -a $PASSWORD SET test_key "failover_test_$(date +%s)"
TEST_VALUE=$(kubectl exec -n $NAMESPACE $CURRENT_MASTER -c redis -- redis-cli -a $PASSWORD GET test_key)
echo "   Wrote value: $TEST_VALUE"
echo ""

echo "4. Simulating master failure (deleting pod)..."
kubectl delete pod -n $NAMESPACE $CURRENT_MASTER --grace-period=0 --force
echo "   Pod deleted: $CURRENT_MASTER"
echo ""

echo "5. Waiting for new master election (max 60s)..."
for i in {1..12}; do
    sleep 5
    NEW_MASTER=$(get_master)
    if [ "$NEW_MASTER" != "none" ] && [ "$NEW_MASTER" != "$CURRENT_MASTER" ]; then
        echo "   New master elected: $NEW_MASTER (after ${i}x5 seconds)"
        break
    fi
    echo "   Waiting... ($i/12)"
done

if [ "$NEW_MASTER" = "none" ] || [ "$NEW_MASTER" = "$CURRENT_MASTER" ]; then
    echo "   ERROR: No new master elected!"
    exit 1
fi

echo ""

echo "6. Verifying new master role..."
sleep 5
NEW_ROLE=$(check_role $NEW_MASTER)
echo "   New master Redis role: $NEW_ROLE"
echo ""

echo "7. Checking if test data survived (if replication was working)..."
SURVIVED_VALUE=$(kubectl exec -n $NAMESPACE $NEW_MASTER -c redis -- redis-cli -a $PASSWORD GET test_key 2>/dev/null || echo "not found")
echo "   Retrieved value: $SURVIVED_VALUE"
echo ""

echo "8. Summary:"
echo "   Old master: $CURRENT_MASTER"
echo "   New master: $NEW_MASTER"
echo "   Failover: SUCCESS"
echo ""

echo "9. Current pod status:"
kubectl get pods -n $NAMESPACE -l app=redis
echo ""

echo "Test complete!"

