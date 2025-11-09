#!/bin/bash

# Create a test HostedCluster for local operator validation
#
# Usage:
#   create-cluster.sh [OPTIONS]
#
# Options:
#   --cluster-name NAME                    Cluster name (default: from CLUSTER_NAME env var)
#   --namespace NAMESPACE                  Namespace (default: from NAMESPACE env var or 'clusters')
#   --replicas COUNT                       Node pool replicas (default: 1)
#   --release-image IMAGE                  OpenShift release image
#   --control-plane-operator-image IMAGE   Custom control plane operator image
#   --help                                 Show this help message
#
# Environment Variables (required):
#   CLUSTER_NAME            Base cluster name (will be timestamped)
#   BASE_DOMAIN             Base domain for cluster
#   PULL_SECRET             Path to pull secret file
#   REGION                  AWS region
#   AWS_CREDS               Path to AWS credentials file
#
# Environment Variables (optional):
#   NAMESPACE               Namespace for cluster (default: clusters)
#   REPLICAS                Number of node pool replicas (default: 1)
#   RELEASE_IMAGE           OpenShift release image (default: 4.19.18)
#   CONTROL_PLANE_OPERATOR_IMAGE  Custom CPO image
#   TEST_CLUSTER_INFO       Cluster info file (default: .test-cluster.info)
#
# Exit codes:
#   0 - Success
#   1 - Error
#   2 - Usage error

set -euo pipefail

# Default values
NAMESPACE="${NAMESPACE:-clusters}"
REPLICAS="${REPLICAS:-1}"
RELEASE_IMAGE="${RELEASE_IMAGE:-quay.io/openshift-release-dev/ocp-release:4.19.18-x86_64}"
TEST_CLUSTER_INFO="${TEST_CLUSTER_INFO:-.test-cluster.info}"
CONTROL_PLANE_OPERATOR_IMAGE="${CONTROL_PLANE_OPERATOR_IMAGE:-}"

# Parse arguments
while [[ $# -gt 0 ]]; do
	case $1 in
		--cluster-name)
			CLUSTER_NAME="$2"
			shift 2
			;;
		--namespace)
			NAMESPACE="$2"
			shift 2
			;;
		--replicas)
			REPLICAS="$2"
			shift 2
			;;
		--release-image)
			RELEASE_IMAGE="$2"
			shift 2
			;;
		--control-plane-operator-image)
			CONTROL_PLANE_OPERATOR_IMAGE="$2"
			shift 2
			;;
		--help|-h)
			echo "Usage: $0 [OPTIONS]"
			echo ""
			echo "Create a test HostedCluster for local operator validation"
			echo ""
			echo "Options:"
			echo "  --cluster-name NAME                    Cluster name (required if CLUSTER_NAME not set)"
			echo "  --namespace NAMESPACE                  Namespace (default: clusters)"
			echo "  --replicas COUNT                       Node pool replicas (default: 1)"
			echo "  --release-image IMAGE                  OpenShift release image"
			echo "  --control-plane-operator-image IMAGE   Custom CPO image"
			echo "  --help                                 Show this help message"
			echo ""
			echo "Required Environment Variables:"
			echo "  CLUSTER_NAME            Base cluster name"
			echo "  BASE_DOMAIN             Base domain for cluster"
			echo "  PULL_SECRET             Path to pull secret file"
			echo "  REGION                  AWS region"
			echo "  AWS_CREDS               Path to AWS credentials file"
			echo ""
			echo "Optional Environment Variables:"
			echo "  NAMESPACE               Namespace (default: clusters)"
			echo "  REPLICAS                Replicas (default: 1)"
			echo "  RELEASE_IMAGE           Release image (default: 4.19.18)"
			exit 0
			;;
		*)
			echo "✗ Unknown option: $1" >&2
			echo "Run '$0 --help' for usage" >&2
			exit 2
			;;
	esac
done

# Validate required environment variables
if [ -z "${CLUSTER_NAME:-}" ]; then
	echo "✗ ERROR: CLUSTER_NAME not set"
	echo "  Provide via --cluster-name or set CLUSTER_NAME environment variable"
	echo "  Tip: Source your .envrc file"
	exit 1
fi

if [ -z "${BASE_DOMAIN:-}" ] || [ -z "${PULL_SECRET:-}" ] || [ -z "${REGION:-}" ] || [ -z "${AWS_CREDS:-}" ]; then
	echo "✗ ERROR: Required variables not set"
	echo "  Need: BASE_DOMAIN, PULL_SECRET, REGION, AWS_CREDS"
	echo "  Tip: Source your .envrc file"
	exit 1
fi

# Get script directory and repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Generate timestamped cluster name
TIMESTAMP=$(date +%s)
TEST_CLUSTER_NAME="$CLUSTER_NAME-test-local-$TIMESTAMP"

# Create temporary directory for manifests
MANIFEST_DIR=$(mktemp -d /tmp/hypershift-test-cluster-XXXXXX)

echo "Creating test cluster..."
echo "  Name: $TEST_CLUSTER_NAME"
echo "  Namespace: $NAMESPACE"
echo "  Replicas: $REPLICAS"
echo "  Release Image: $RELEASE_IMAGE"
echo "  Manifest directory: $MANIFEST_DIR"

# Build hypershift create command
HYPERSHIFT_CREATE_CMD=(
	"$REPO_ROOT/bin/hypershift" create cluster aws
	--name "$TEST_CLUSTER_NAME"
	--node-pool-replicas="$REPLICAS"
	--base-domain "$BASE_DOMAIN"
	--pull-secret "$PULL_SECRET"
	--region "$REGION"
	--release-image "$RELEASE_IMAGE"
	--generate-ssh
	--aws-creds "$AWS_CREDS"
	--namespace "$NAMESPACE"
	--public-only
	--render-into "$MANIFEST_DIR/manifests.yaml"
	--render-sensitive true
)

# Add optional control plane operator image
if [ -n "$CONTROL_PLANE_OPERATOR_IMAGE" ]; then
	HYPERSHIFT_CREATE_CMD+=(--control-plane-operator-image "$CONTROL_PLANE_OPERATOR_IMAGE")
fi

echo "Generating manifests..."
"${HYPERSHIFT_CREATE_CMD[@]}"

echo "  Manifests: $MANIFEST_DIR/manifests.yaml"
echo "Applying manifests..."
kubectl apply -f "$MANIFEST_DIR/manifests.yaml"

# Wait a moment for cluster to be created
sleep 2

# Try to get infraID
INFRA_ID=$(kubectl get -n "$NAMESPACE" hc "$TEST_CLUSTER_NAME" -o jsonpath='{.spec.infraID}' 2>/dev/null || echo "")
if [ -z "$INFRA_ID" ]; then
	echo "⚠ WARNING: Could not get infraID yet (cluster may still be initializing)"
	INFRA_ID="unknown"
fi

# Save cluster info for cleanup
cat > "$TEST_CLUSTER_INFO" <<EOF
CLUSTER_NAME=$TEST_CLUSTER_NAME
INFRA_ID=$INFRA_ID
MANIFEST_DIR=$MANIFEST_DIR
NAMESPACE=$NAMESPACE
EOF

echo ""
echo "✓ Test cluster created"
echo "  Name: $TEST_CLUSTER_NAME"
echo "  Namespace: $NAMESPACE"
echo "  InfraID: $INFRA_ID"
echo "  Manifests: $MANIFEST_DIR/manifests.yaml"
echo ""
echo "Next: Run 'make validate-local-operator' to verify operator is reconciling"
