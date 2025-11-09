#!/bin/bash

# Destroy test HostedCluster and clean up AWS resources
#
# Usage:
#   destroy-cluster.sh [OPTIONS]
#
# Options:
#   --force    Skip confirmation prompts
#   --help     Show this help message
#
# Environment Variables (required):
#   AWS_CREDS             Path to AWS credentials file
#   BASE_DOMAIN           Base domain for cluster
#
# Environment Variables (optional):
#   TEST_CLUSTER_INFO     Cluster info file (default: .test-cluster.info)
#
# Exit codes:
#   0 - Success
#   1 - Error
#   2 - Usage error

set -euo pipefail

# Default values
TEST_CLUSTER_INFO="${TEST_CLUSTER_INFO:-.test-cluster.info}"
FORCE=false

# Parse arguments
while [[ $# -gt 0 ]]; do
	case $1 in
		--force)
			FORCE=true
			shift
			;;
		--help|-h)
			echo "Usage: $0 [--force]"
			echo ""
			echo "Destroy test HostedCluster and clean up AWS resources"
			echo ""
			echo "Options:"
			echo "  --force    Skip confirmation prompts"
			echo "  --help     Show this help message"
			echo ""
			echo "Required Environment Variables:"
			echo "  AWS_CREDS             Path to AWS credentials file"
			echo "  BASE_DOMAIN           Base domain for cluster"
			echo ""
			echo "Optional Environment Variables:"
			echo "  TEST_CLUSTER_INFO     Cluster info file (default: .test-cluster.info)"
			exit 0
			;;
		*)
			echo "✗ Unknown option: $1" >&2
			echo "Run '$0 --help' for usage" >&2
			exit 2
			;;
	esac
done

# Check if cluster info file exists
if [ ! -f "$TEST_CLUSTER_INFO" ]; then
	echo "✗ ERROR: No test cluster info found. Nothing to destroy."
	echo "  File $TEST_CLUSTER_INFO does not exist"
	exit 1
fi

# Validate required environment variables
if [ -z "${AWS_CREDS:-}" ] || [ -z "${BASE_DOMAIN:-}" ]; then
	echo "✗ ERROR: Required environment variables not set"
	echo "  Need: AWS_CREDS, BASE_DOMAIN"
	echo "  Tip: Source your .envrc file"
	exit 1
fi

# Get script directory and repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Read cluster info
echo "Reading test cluster info..."
source "./$TEST_CLUSTER_INFO"

# Validate required variables from cluster info
if [ -z "${CLUSTER_NAME:-}" ] || [ -z "${NAMESPACE:-}" ]; then
	echo "✗ ERROR: Invalid cluster info file"
	echo "  Missing CLUSTER_NAME or NAMESPACE"
	exit 1
fi

echo "Destroying test cluster: $CLUSTER_NAME"
echo "  Namespace: $NAMESPACE"
echo "  InfraID: ${INFRA_ID:-unknown}"

# If infraID is unknown, try to fetch it
if [ "${INFRA_ID:-unknown}" = "unknown" ]; then
	echo "⚠ InfraID was unknown at creation, attempting to fetch..."
	INFRA_ID=$(kubectl get -n "$NAMESPACE" hc "$CLUSTER_NAME" -o jsonpath='{.spec.infraID}' 2>/dev/null || echo "")

	if [ -z "$INFRA_ID" ]; then
		echo "✗ Cannot get infraID. You may need to manually clean up AWS resources."
		echo "  Deleting cluster manifest anyway..."
		kubectl delete -n "$NAMESPACE" hc "$CLUSTER_NAME" --ignore-not-found=true
		kubectl delete -n "$NAMESPACE" np --selector=hypershift.openshift.io/hosted-cluster="$CLUSTER_NAME" --ignore-not-found=true
		rm -f "$TEST_CLUSTER_INFO"
		exit 1
	fi
fi

# Confirm unless --force
if [ "$FORCE" != true ]; then
	echo ""
	read -p "This will destroy cluster '$CLUSTER_NAME' and all AWS resources. Continue? (y/N) " -n 1 -r
	echo
	if [[ ! $REPLY =~ ^[Yy]$ ]]; then
		echo "Aborted"
		exit 0
	fi
fi

# Destroy cluster
"$REPO_ROOT/bin/hypershift" destroy cluster aws \
	--namespace "$NAMESPACE" \
	--name "$CLUSTER_NAME" \
	--aws-creds "$AWS_CREDS" \
	--base-domain "$BASE_DOMAIN" \
	--infra-id "$INFRA_ID"

echo ""
if [ -n "${MANIFEST_DIR:-}" ] && [ -f "$MANIFEST_DIR/manifests.yaml" ]; then
	echo "Manifests preserved at: $MANIFEST_DIR/manifests.yaml"
fi
echo "✓ Test cluster destroyed"

# Clean up cluster info file
rm -f "$TEST_CLUSTER_INFO"
