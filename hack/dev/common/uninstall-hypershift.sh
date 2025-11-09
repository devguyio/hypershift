#!/bin/bash

# Uninstall HyperShift development installation
#
# Usage:
#   uninstall-hypershift.sh [--force]
#
# Options:
#   --force    Skip confirmation prompts
#
# Environment Variables:
#   INSTALL_MANIFEST_TRACKER   Location of manifest tracker file (default: .hypershift-install.yaml)
#
# Exit codes:
#   0 - Success
#   1 - Error
#   2 - Usage error

set -euo pipefail

# Default values
INSTALL_MANIFEST_TRACKER="${INSTALL_MANIFEST_TRACKER:-.hypershift-install.yaml}"
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
			echo "Uninstall HyperShift development installation"
			echo ""
			echo "Options:"
			echo "  --force    Skip confirmation prompts"
			echo "  --help     Show this help message"
			echo ""
			echo "Environment Variables:"
			echo "  INSTALL_MANIFEST_TRACKER   Manifest tracker file (default: .hypershift-install.yaml)"
			exit 0
			;;
		*)
			echo "✗ Unknown option: $1" >&2
			echo "Run '$0 --help' for usage" >&2
			exit 2
			;;
	esac
done

# Check if installation manifest tracker exists
if [ ! -f "$INSTALL_MANIFEST_TRACKER" ]; then
	echo "✗ ERROR: No installation manifest found"
	echo "  File $INSTALL_MANIFEST_TRACKER does not exist"
	echo "  HyperShift may not be installed or was installed manually"
	exit 1
fi

# Read manifest location
MANIFEST_FILE=$(cat "$INSTALL_MANIFEST_TRACKER")

# Check if manifest file exists
if [ ! -f "$MANIFEST_FILE" ]; then
	echo "⚠ WARNING: Manifest file not found: $MANIFEST_FILE"
	echo "  Cleaning up tracker file anyway..."
	rm -f "$INSTALL_MANIFEST_TRACKER"
	exit 1
fi

# Confirm unless --force
if [ "$FORCE" != true ]; then
	echo "This will uninstall HyperShift using manifest: $MANIFEST_FILE"
	read -p "Continue? (y/N) " -n 1 -r
	echo
	if [[ ! $REPLY =~ ^[Yy]$ ]]; then
		echo "Aborted"
		exit 0
	fi
fi

# Uninstall
echo "Uninstalling HyperShift..."
echo "  Manifest: $MANIFEST_FILE"
kubectl delete -f "$MANIFEST_FILE" --ignore-not-found=true
echo ""
echo "✓ HyperShift uninstalled"
echo "  Manifest preserved at: $MANIFEST_FILE"

# Clean up tracker
rm -f "$INSTALL_MANIFEST_TRACKER"
