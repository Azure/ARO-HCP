#!/bin/bash
# Script to set synchronized OpenShift versions for E2E tests
# This ensures control plane and node pool use the same version

set -euo pipefail

# Parse command line arguments
CHANNEL_GROUP="${1:-candidate}"
VERSION_MINOR="${2:-4.20}"

echo "=== Setting OpenShift Versions for E2E Tests ==="
echo "Channel Group: $CHANNEL_GROUP"
echo "Version Minor: $VERSION_MINOR"
echo ""

# Resolve the latest version for the channel
echo "Fetching latest version from Cincinnati..."

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

    local version=$(curl --silent --show-error --fail --location --retry 3 --retry-delay 2 --retry-connrefused --max-time 30 "$graph_url" | jq -r '.nodes[].version' | sort -V | tail -1)

    if [ -z "$version" ]; then
        echo "ERROR: No version found for channel $channel-$minor" >&2
        return 1
    fi

    echo "$version"
}

# Get the version
VERSION=$(get_latest_version "$CHANNEL_GROUP" "$VERSION_MINOR")

if [ -z "$VERSION" ]; then
    echo "ERROR: Failed to resolve version" >&2
    exit 1
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
