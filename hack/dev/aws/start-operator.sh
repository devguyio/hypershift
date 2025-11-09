#!/bin/bash

# Start HyperShift operator locally with AWS configuration
#
# Usage:
#   start-operator.sh [OPTIONS]
#
# Options:
#   --control-plane-operator-image IMAGE   Custom control plane operator image
#   --help                                  Show this help message
#
# Environment Variables (required):
#   KUBECONFIG              Path to management cluster kubeconfig
#   BUCKET_NAME             S3 bucket for OIDC storage
#   AWS_CREDS               Path to AWS credentials file
#   REGION                  AWS region
#
# Environment Variables (optional):
#   PID_FILE                PID file location (default: .local-operator.pid)
#   LOG_FILE                Log file location (default: /tmp/hypershift-operator-local-<timestamp>.log)
#
# Exit codes:
#   0 - Success
#   1 - Error
#   2 - Usage error

set -euo pipefail

# Default values
PID_FILE="${PID_FILE:-.local-operator.pid}"
LOG_FILE="${LOG_FILE:-/tmp/hypershift-operator-local-$(date +%Y%m%d-%H%M%S).log}"
CONTROL_PLANE_OPERATOR_IMAGE="quay.io/hypershift/hypershift:latest"

# Parse arguments
while [[ $# -gt 0 ]]; do
	case $1 in
		--control-plane-operator-image)
			CONTROL_PLANE_OPERATOR_IMAGE="$2"
			shift 2
			;;
		--help|-h)
			echo "Usage: $0 [OPTIONS]"
			echo ""
			echo "Start HyperShift operator locally with AWS configuration"
			echo ""
			echo "Options:"
			echo "  --control-plane-operator-image IMAGE   Custom control plane operator image"
			echo "  --help                                  Show this help message"
			echo ""
			echo "Required Environment Variables:"
			echo "  KUBECONFIG              Path to management cluster kubeconfig"
			echo "  BUCKET_NAME             S3 bucket for OIDC storage"
			echo "  AWS_CREDS               Path to AWS credentials file"
			echo "  REGION                  AWS region"
			echo ""
			echo "Optional Environment Variables:"
			echo "  PID_FILE                PID file location (default: .local-operator.pid)"
			echo "  LOG_FILE                Log file location (default: auto-generated)"
			exit 0
			;;
		*)
			echo "✗ Unknown option: $1" >&2
			echo "Run '$0 --help' for usage" >&2
			exit 2
			;;
	esac
done

# Check if already running
if [ -f "$PID_FILE" ] && ps -p $(cat "$PID_FILE") > /dev/null 2>&1; then
	echo "Local operator already running (PID: $(cat "$PID_FILE"))"
	LATEST_LOG=$(ls -t /tmp/hypershift-operator-local-*.log 2>/dev/null | head -1 || echo "")
	if [ -n "$LATEST_LOG" ]; then
		echo "Logs: $LATEST_LOG"
	fi
	exit 0
fi

# Validate required environment variables
if [ -z "${KUBECONFIG:-}" ]; then
	echo "✗ ERROR: KUBECONFIG environment variable is not set"
	echo "  Please set KUBECONFIG to your management cluster kubeconfig"
	exit 1
fi

if [ -z "${BUCKET_NAME:-}" ] || [ -z "${AWS_CREDS:-}" ] || [ -z "${REGION:-}" ]; then
	echo "✗ ERROR: Required OIDC environment variables not set"
	echo "  Please set: BUCKET_NAME, AWS_CREDS, REGION"
	echo "  Tip: Source your .envrc file or export these variables"
	exit 1
fi

# Get script directory and repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

echo "Starting local HyperShift operator..."

# Start operator in background
nohup env MY_NAMESPACE=hypershift MY_NAME=operator-local \
	KUBECONFIG="$KUBECONFIG" \
	AWS_SHARED_CREDENTIALS_FILE="$AWS_CREDS" \
	AWS_REGION="$REGION" \
	AWS_SDK_LOAD_CONFIG=1 \
	"$REPO_ROOT/bin/hypershift-operator" run \
	--control-plane-operator-image="$CONTROL_PLANE_OPERATOR_IMAGE" \
	--namespace=hypershift \
	--pod-name=operator-local \
	--metrics-addr=0 \
	--enable-ocp-cluster-monitoring=false \
	--oidc-storage-provider-s3-bucket-name="$BUCKET_NAME" \
	--oidc-storage-provider-s3-credentials="$AWS_CREDS" \
	--oidc-storage-provider-s3-region="$REGION" \
	--private-platform=AWS \
	> "$LOG_FILE" 2>&1 &

# Save PID
echo $! > "$PID_FILE"

# Wait a moment and verify it started
sleep 2

if ps -p $(cat "$PID_FILE") > /dev/null 2>&1; then
	echo "✓ Local operator started successfully"
	echo "  PID: $(cat "$PID_FILE")"
	echo "  Logs: $LOG_FILE"
else
	echo "✗ Failed to start operator. Check logs: $LOG_FILE"
	rm -f "$PID_FILE"
	exit 1
fi
