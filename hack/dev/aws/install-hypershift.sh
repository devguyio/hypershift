#!/bin/bash

# Install HyperShift in development mode with AWS OIDC configuration
#
# Usage:
#   install-hypershift.sh [OPTIONS]
#
# Options:
#   --help     Show this help message
#
# Environment Variables (required):
#   KUBECONFIG              Path to management cluster kubeconfig
#   BUCKET_NAME             S3 bucket for OIDC storage
#   AWS_CREDS               Path to AWS credentials file
#   REGION                  AWS region
#
# Environment Variables (optional):
#   INSTALL_MANIFEST_TRACKER  Tracker file location (default: .hypershift-install.yaml)
#
# Exit codes:
#   0 - Success
#   1 - Error
#   2 - Usage error

set -euo pipefail

# Default values
INSTALL_MANIFEST_TRACKER="${INSTALL_MANIFEST_TRACKER:-.hypershift-install.yaml}"

# Parse arguments
while [[ $# -gt 0 ]]; do
	case $1 in
		--help|-h)
			echo "Usage: $0"
			echo ""
			echo "Install HyperShift in development mode with AWS OIDC configuration"
			echo ""
			echo "Required Environment Variables:"
			echo "  KUBECONFIG              Path to management cluster kubeconfig"
			echo "  BUCKET_NAME             S3 bucket for OIDC storage"
			echo "  AWS_CREDS               Path to AWS credentials file"
			echo "  REGION                  AWS region"
			echo ""
			echo "Optional Environment Variables:"
			echo "  INSTALL_MANIFEST_TRACKER  Tracker file (default: .hypershift-install.yaml)"
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
if [ -z "${KUBECONFIG:-}" ]; then
	echo "✗ ERROR: KUBECONFIG environment variable is not set"
	echo "  Please set KUBECONFIG to your management cluster kubeconfig"
	exit 1
fi

if [ -z "${BUCKET_NAME:-}" ] || [ -z "${AWS_CREDS:-}" ] || [ -z "${REGION:-}" ]; then
	echo "✗ ERROR: Required environment variables not set"
	echo "  Please set: BUCKET_NAME, AWS_CREDS, REGION"
	echo "  Tip: Source your .envrc file"
	exit 1
fi

# Get script directory and repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Generate manifest file path
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
MANIFEST_FILE="/tmp/hypershift-install-$TIMESTAMP.yaml"

echo "Installing HyperShift in development mode..."
echo "  Rendering manifests to: $MANIFEST_FILE"

# Render installation manifests
"$REPO_ROOT/bin/hypershift" install render \
	--development \
	--oidc-storage-provider-s3-bucket-name "$BUCKET_NAME" \
	--oidc-storage-provider-s3-credentials "$AWS_CREDS" \
	--oidc-storage-provider-s3-region "$REGION" \
	--private-platform AWS \
	--aws-private-creds "$AWS_CREDS" \
	--aws-private-region "$REGION" \
	> "$MANIFEST_FILE"

echo "  Applying manifests..."
kubectl apply -f "$MANIFEST_FILE"

# Track installation for cleanup
echo "$MANIFEST_FILE" > "$INSTALL_MANIFEST_TRACKER"

echo ""
echo "✓ HyperShift installed in development mode"
echo "  Manifests: $MANIFEST_FILE"
echo ""
echo "Next steps:"
echo "  1. Run: make start-operator-locally"
echo "  2. Validate: make validate-local-operator"
