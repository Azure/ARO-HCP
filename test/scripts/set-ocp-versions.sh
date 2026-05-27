#!/bin/bash
# Script to set synchronized OpenShift versions for E2E tests
# This ensures control plane and node pool use the same version
# Compatible with Linux and macOS (automatically detects available sorting methods)

# Detect if script is being sourced or executed
# When sourced, preserve parent shell options to avoid mutating caller's environment
(return 0 2>/dev/null) && SOURCED=1 || SOURCED=0

# Preserve shell options if sourced and set up restoration trap
if [ "$SOURCED" = "1" ]; then
    # Save current shell options
    OLD_SHELL_OPTS=$(set +o)
    # Trap to restore shell options on exit/return, even on error
    trap 'eval "$OLD_SHELL_OPTS"' RETURN ERR EXIT
fi

set -euo pipefail

# Helper function to exit/return appropriately
script_exit() {
    local exit_code=$1
    if [ "$SOURCED" = "1" ]; then
        # Restore shell options before returning
        eval "$OLD_SHELL_OPTS"
        return "$exit_code"
    else
        exit "$exit_code"
    fi
}

# Parse command line arguments
CHANNEL_GROUP="${1:-candidate}"
VERSION_MINOR="${2:-4.20}"

echo "=== Setting OpenShift Versions for E2E Tests ==="
echo "Channel Group: $CHANNEL_GROUP"
echo "Version Minor: $VERSION_MINOR"
echo ""

# Resolve the latest version for the channel
echo "Fetching latest version from Cincinnati..."

# Portable version sorting function
# Works on both Linux (GNU sort) and macOS (BSD sort)
sort_versions() {
    # Try sort -V (GNU sort, available on Linux and via coreutils on macOS)
    if sort -V /dev/null &>/dev/null 2>&1; then
        sort -V
    # Try gsort -V (GNU sort via Homebrew coreutils on macOS)
    elif command -v gsort &>/dev/null && gsort -V /dev/null &>/dev/null 2>&1; then
        gsort -V
    # Fallback: use Python for semantic version sorting
    elif command -v python3 &>/dev/null; then
        python3 -c '
import sys
from packaging import version
versions = [line.strip() for line in sys.stdin if line.strip()]
try:
    sorted_versions = sorted(versions, key=lambda v: version.parse(v))
    for v in sorted_versions:
        print(v)
except:
    # Fallback to basic string sort if packaging module not available
    for v in sorted(versions):
        print(v)
'
    else
        # Last resort: basic alphanumeric sort (not semver-aware, but better than nothing)
        echo "WARNING: No proper version sorting available. Install GNU coreutils or Python with packaging module for accurate results." >&2
        sort
    fi
}

# Function to get latest version (simplified - you may want to use the Go helper)
get_latest_version() {
    local channel="$1"
    local minor="$2"

    # Query the OpenShift graph API
    local graph_url="https://api.openshift.com/api/upgrades_info/v1/graph?channel=${channel}-${minor}"

    # Fetch and parse (requires jq)
    if ! command -v jq &> /dev/null; then
        echo "ERROR: jq is required but not installed" >&2
        return 1
    fi

    local version=$(curl --silent --show-error --fail --location --retry 3 --retry-delay 2 --retry-connrefused --max-time 30 "$graph_url" | jq -r '.nodes[].version' | sort_versions | tail -1)

    if [ -z "$version" ]; then
        echo "ERROR: No version found for channel $channel-$minor" >&2
        return 1
    fi

    echo "$version"
}

# Get the version
# Note: We capture both the output and the exit status to handle errors properly
if ! VERSION=$(get_latest_version "$CHANNEL_GROUP" "$VERSION_MINOR"); then
    echo "ERROR: Failed to fetch version from Cincinnati" >&2
    script_exit 1
fi

if [ -z "$VERSION" ]; then
    echo "ERROR: Failed to resolve version" >&2
    script_exit 1
fi

echo "Resolved Version: $VERSION"
echo ""

# Export environment variables
echo "Setting environment variables..."
export ARO_HCP_OPENSHIFT_CHANNEL_GROUP="$CHANNEL_GROUP"
export ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP="$CHANNEL_GROUP"
export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION="$VERSION"
export ARO_HCP_OPENSHIFT_NODEPOOL_VERSION="$VERSION"

# Print what was set
echo ""
echo "✓ Environment variables set:"
echo "  export ARO_HCP_OPENSHIFT_CHANNEL_GROUP=\"$CHANNEL_GROUP\""
echo "  export ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP=\"$CHANNEL_GROUP\""
echo "  export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION=\"$VERSION\""
echo "  export ARO_HCP_OPENSHIFT_NODEPOOL_VERSION=\"$VERSION\""
echo ""
echo "To apply these in your current shell, run:"
echo "  source ${BASH_SOURCE[0]} $CHANNEL_GROUP $VERSION_MINOR"
echo ""
echo "Or copy and paste:"
cat <<EOF
export ARO_HCP_OPENSHIFT_CHANNEL_GROUP="$CHANNEL_GROUP"
export ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP="$CHANNEL_GROUP"
export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION="$VERSION"
export ARO_HCP_OPENSHIFT_NODEPOOL_VERSION="$VERSION"
EOF

# Restore shell options if sourced
if [ "$SOURCED" = "1" ]; then
    eval "$OLD_SHELL_OPTS"
fi
