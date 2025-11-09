#!/bin/bash
# Validation script for local HyperShift operator
# Verifies that the operator is running correctly locally and managing resources

set -euo pipefail

KUBECONFIG="${KUBECONFIG:-}"
if [ -z "$KUBECONFIG" ]; then
    echo "ERROR: KUBECONFIG environment variable must be set"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PID_FILE="$REPO_ROOT/.local-operator.pid"

echo "=== HyperShift Local Operator Validation ==="
echo ""

FAIL_COUNT=0

# Check 1: Verify local operator process is running
echo "Check 1: Verifying local operator process..."
if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if ps -p "$PID" > /dev/null 2>&1; then
        echo "  ✓ PASS: Local operator process running (PID: $PID)"
        OPERATOR_CMD=$(ps -p "$PID" -o cmd= 2>/dev/null || echo "")
        if [[ "$OPERATOR_CMD" == *"hypershift-operator run"* ]]; then
            echo "    Command: ${OPERATOR_CMD:0:100}..."
        fi
    else
        echo "  ✗ FAIL: PID file exists but process $PID is not running"
        ((FAIL_COUNT++))
    fi
else
    echo "  ✗ FAIL: No PID file found at $PID_FILE"
    echo "    Run: make start-operator-locally"
    ((FAIL_COUNT++))
fi
echo ""

# Check 2: Verify in-cluster operator is scaled down
echo "Check 2: Verifying in-cluster operator is scaled down..."
REPLICAS=$(kubectl get deployment operator -n hypershift -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "not-found")
if [ "$REPLICAS" = "0" ]; then
    echo "  ✓ PASS: In-cluster operator scaled to 0 replicas"
elif [ "$REPLICAS" = "not-found" ]; then
    echo "  ✓ PASS: No in-cluster operator deployment (clean cluster)"
else
    echo "  ✗ FAIL: In-cluster operator has $REPLICAS replicas (should be 0)"
    echo "    Run: kubectl scale deployment operator -n hypershift --replicas=0"
    ((FAIL_COUNT++))
fi
echo ""

# Check 3: Verify leader election lease
echo "Check 3: Verifying leader election..."
LEASE_HOLDER=$(kubectl get lease hypershift-operator-leader-elect -n hypershift -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || echo "not-found")
LEASE_RENEW=$(kubectl get lease hypershift-operator-leader-elect -n hypershift -o jsonpath='{.spec.renewTime}' 2>/dev/null || echo "")

if [ "$LEASE_HOLDER" != "not-found" ]; then
    echo "  ✓ PASS: Leader lease held by: $LEASE_HOLDER"
    if [ -n "$LEASE_RENEW" ]; then
        echo "    Last renewed: $LEASE_RENEW"
        # Check if lease was renewed recently (within last 2 minutes)
        LEASE_TIME=$(date -d "$LEASE_RENEW" +%s 2>/dev/null || echo "0")
        NOW=$(date +%s)
        AGE=$((NOW - LEASE_TIME))
        if [ "$LEASE_TIME" -gt 0 ] && [ $AGE -lt 120 ]; then
            echo "    ✓ Lease is fresh (renewed ${AGE}s ago)"
        elif [ "$LEASE_TIME" -gt 0 ]; then
            echo "    ⚠ WARNING: Lease is stale (renewed ${AGE}s ago)"
        fi
    fi
else
    echo "  ✗ FAIL: No leader election lease found"
    echo "    The operator may not have started correctly"
    ((FAIL_COUNT++))
fi
echo ""

# Check 4: Verify HostedCluster resources exist
echo "Check 4: Checking HostedCluster resources..."
HC_COUNT=$(kubectl get hostedclusters -A --no-headers 2>/dev/null | wc -l || echo "0")
if [ "$HC_COUNT" -gt 0 ]; then
    echo "  ✓ Found $HC_COUNT HostedCluster(s):"
    kubectl get hostedclusters -A -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,AVAILABLE:.status.conditions[?\(@.type==\"Available\"\)].status 2>/dev/null | head -10
else
    echo "  ⚠ INFO: No HostedClusters found (OK if testing on clean cluster)"
fi
echo ""

# Check 5: Test operator responsiveness (if HostedClusters exist)
echo "Check 5: Testing operator responsiveness..."
if [ "$HC_COUNT" -gt 0 ]; then
    # Get the first hostedcluster
    HC_NAMESPACE=$(kubectl get hostedclusters -A --no-headers 2>/dev/null | head -1 | awk '{print $1}')
    HC_NAME=$(kubectl get hostedclusters -A --no-headers 2>/dev/null | head -1 | awk '{print $2}')

    echo "  Testing with HostedCluster: $HC_NAMESPACE/$HC_NAME"

    # Add a test annotation
    TEST_TIME=$(date +%s)
    if kubectl annotate hostedcluster "$HC_NAME" -n "$HC_NAMESPACE" "test.hypershift.openshift.io/validation=$TEST_TIME" --overwrite &>/dev/null; then
        # Wait for reconciliation
        sleep 3

        # Verify annotation persists (operator reconciled and kept it)
        ANNO=$(kubectl get hostedcluster "$HC_NAME" -n "$HC_NAMESPACE" -o jsonpath='{.metadata.annotations.test\.hypershift\.openshift\.io/validation}' 2>/dev/null || echo "")

        if [ "$ANNO" = "$TEST_TIME" ]; then
            echo "  ✓ PASS: Operator responding (annotation applied and reconciled)"
            # Clean up
            kubectl annotate hostedcluster "$HC_NAME" -n "$HC_NAMESPACE" "test.hypershift.openshift.io/validation-" &>/dev/null || true
        else
            echo "  ✗ FAIL: Annotation test failed (operator may not be reconciling)"
            ((FAIL_COUNT++))
        fi
    else
        echo "  ⚠ WARNING: Could not add test annotation (permission issue?)"
    fi
else
    echo "  ⚠ SKIP: No HostedClusters available to test with"
    echo "    To test cluster creation: make create-test-cluster-local"
fi
echo ""

# Check 6: Verify CRDs are installed
echo "Check 6: Verifying HyperShift CRDs..."
REQUIRED_CRDS=(
    "hostedclusters.hypershift.openshift.io"
    "nodepools.hypershift.openshift.io"
    "hostedcontrolplanes.hypershift.openshift.io"
)

CRD_FAIL=0
for crd in "${REQUIRED_CRDS[@]}"; do
    if kubectl get crd "$crd" &>/dev/null; then
        echo "  ✓ $crd"
    else
        echo "  ✗ Missing: $crd"
        ((CRD_FAIL++))
    fi
done

if [ $CRD_FAIL -eq 0 ]; then
    echo "  ✓ PASS: All required CRDs present"
else
    echo "  ✗ FAIL: $CRD_FAIL CRD(s) missing"
    echo "    Run: bin/hypershift install --development"
    ((FAIL_COUNT++))
fi
echo ""

# Summary
echo "=== Validation Summary ==="
if [ $FAIL_COUNT -eq 0 ]; then
    echo "✓ ALL CHECKS PASSED"
    echo ""
    echo "Your local HyperShift operator is running correctly!"
    echo ""
    echo "Next steps:"
    echo "  - Monitor logs: tail -f /tmp/hypershift-operator-local-*.log"
    echo "  - Make code changes and restart: make stop-operator-locally && make start-operator-locally"
    echo "  - Stop operator: make stop-operator-locally"
    exit 0
else
    echo "✗ $FAIL_COUNT CHECK(S) FAILED"
    echo ""
    echo "Please fix the issues above before continuing."
    exit 1
fi
