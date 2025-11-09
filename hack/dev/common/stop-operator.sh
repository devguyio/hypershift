#!/bin/bash

# Stop the local HyperShift operator gracefully
#
# Usage:
#   stop-operator.sh [--force]
#
# Options:
#   --force    Immediately kill the operator with SIGKILL
#
# Environment Variables:
#   PID_FILE   Location of PID file (default: .local-operator.pid)
#
# Exit codes:
#   0 - Success
#   1 - Error
#   2 - Usage error

set -euo pipefail

# Default values
PID_FILE="${PID_FILE:-.local-operator.pid}"
FORCE_KILL=false

# Parse arguments
while [[ $# -gt 0 ]]; do
	case $1 in
		--force)
			FORCE_KILL=true
			shift
			;;
		--help|-h)
			echo "Usage: $0 [--force]"
			echo ""
			echo "Stop the local HyperShift operator gracefully"
			echo ""
			echo "Options:"
			echo "  --force    Immediately kill the operator with SIGKILL"
			echo "  --help     Show this help message"
			echo ""
			echo "Environment Variables:"
			echo "  PID_FILE   Location of PID file (default: .local-operator.pid)"
			exit 0
			;;
		*)
			echo "✗ Unknown option: $1" >&2
			echo "Run '$0 --help' for usage" >&2
			exit 2
			;;
	esac
done

# Check if PID file exists
if [ ! -f "$PID_FILE" ]; then
	echo "No PID file found. Operator not running."
	exit 0
fi

# Read PID
PID=$(cat "$PID_FILE")

# Check if process is running
if ! ps -p "$PID" > /dev/null 2>&1; then
	echo "Process $PID not running"
	rm -f "$PID_FILE"
	exit 0
fi

# Stop the operator
if [ "$FORCE_KILL" = true ]; then
	echo "Force killing local operator (PID: $PID)..."
	kill -9 "$PID" 2>/dev/null || true
	echo "✓ Local operator force killed"
else
	echo "Stopping local operator (PID: $PID)..."
	kill "$PID"
	sleep 2

	# Check if still running, force kill if necessary
	if ps -p "$PID" > /dev/null 2>&1; then
		echo "Process still running, force killing..."
		kill -9 "$PID" 2>/dev/null || true
	fi

	echo "✓ Local operator stopped"
fi

# Clean up PID file
rm -f "$PID_FILE"
