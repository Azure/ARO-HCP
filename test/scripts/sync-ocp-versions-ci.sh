#!/bin/bash
# CI/CD script to synchronize OpenShift versions for E2E tests
# This script fetches the version once and exports it for both control plane and node pools
# Designed for use in CI environments (Prow, GitHub Actions, etc.)

# Detect if script is being sourced or executed
# When sourced, preserve parent shell options and use return instead of exit
is_sourced() {
    [[ "${BASH_SOURCE[0]}" != "$0" ]]
}

# Preserve shell options if sourced
if is_sourced; then
    OLD_SHELL_OPTS=$(set +o)
fi

set -euo pipefail

main() {
    # Configuration
    CHANNEL_GROUP="${ARO_HCP_OPENSHIFT_CHANNEL_GROUP:-candidate}"
    VERSION_MINOR="${ARO_HCP_OPENSHIFT_VERSION_MINOR:-4.20}"

    echo "=== OpenShift Version Synchronization for CI ==="
    echo "Channel Group: $CHANNEL_GROUP"
    echo "Version Minor: $VERSION_MINOR"
    echo ""

    # Check if versions are already explicitly set
    if [ -n "${ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION:-}" ] && [ -n "${ARO_HCP_OPENSHIFT_NODEPOOL_VERSION:-}" ]; then
        CP_VERSION="$ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION"
        NP_VERSION="$ARO_HCP_OPENSHIFT_NODEPOOL_VERSION"

        echo "Versions explicitly set via environment:"
        echo "  Control Plane: $CP_VERSION"
        echo "  Node Pool: $NP_VERSION"

        # Validate they match
        if [ "$CP_VERSION" != "$NP_VERSION" ]; then
            echo "ERROR: Control plane and node pool versions differ!" >&2
            echo "  This will cause validation errors." >&2
            echo "  Either unset both variables or ensure they match." >&2
            return 1
        fi

        echo "✓ Versions are synchronized"
        return 0
    fi

    # Fetch latest version from Cincinnati
    echo "Fetching latest version from OpenShift graph API..."

    GRAPH_URL="https://api.openshift.com/api/upgrades_info/v1/graph?channel=${CHANNEL_GROUP}-${VERSION_MINOR}"

    # Use curl with retries for robustness in CI
    MAX_RETRIES=3
    RETRY_DELAY=5

    for i in $(seq 1 $MAX_RETRIES); do
        if GRAPH_JSON=$(curl -s --fail --max-time 30 "$GRAPH_URL" 2>/dev/null); then
            break
        fi

        if [ $i -eq $MAX_RETRIES ]; then
            echo "ERROR: Failed to fetch version after $MAX_RETRIES attempts" >&2
            return 1
        fi

        echo "Attempt $i failed, retrying in ${RETRY_DELAY}s..."
        sleep $RETRY_DELAY
    done

    # Parse latest version (requires jq)
    if ! command -v jq &> /dev/null; then
        echo "ERROR: jq is required but not installed" >&2
        echo "Install with: apt-get install jq (or equivalent)" >&2
        return 1
    fi

    VERSION=$(echo "$GRAPH_JSON" | jq -r '.nodes[].version' | sort -V | tail -1)

    if [ -z "$VERSION" ] || [ "$VERSION" = "null" ]; then
        echo "ERROR: No version found for channel ${CHANNEL_GROUP}-${VERSION_MINOR}" >&2
        echo "Response was: $GRAPH_JSON" >&2
        return 1
    fi

    echo "Resolved Version: $VERSION"
    echo ""

    # Export synchronized versions
    export ARO_HCP_OPENSHIFT_CHANNEL_GROUP="$CHANNEL_GROUP"
    export ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP="$CHANNEL_GROUP"
    export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION="$VERSION"
    export ARO_HCP_OPENSHIFT_NODEPOOL_VERSION="$VERSION"

    # For GitHub Actions - output to GITHUB_ENV
    if [ -n "${GITHUB_ENV:-}" ]; then
        echo "Exporting to GitHub Actions environment..."
        {
            echo "ARO_HCP_OPENSHIFT_CHANNEL_GROUP=$CHANNEL_GROUP"
            echo "ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP=$CHANNEL_GROUP"
            echo "ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION=$VERSION"
            echo "ARO_HCP_OPENSHIFT_NODEPOOL_VERSION=$VERSION"
        } >> "$GITHUB_ENV"
    fi

    echo "✓ Synchronized versions set:"
    echo "  Channel Group: $CHANNEL_GROUP"
    echo "  Control Plane Version: $VERSION"
    echo "  Node Pool Version: $VERSION"
    echo ""

    # Output for other CI systems
    echo "export ARO_HCP_OPENSHIFT_CHANNEL_GROUP=\"$CHANNEL_GROUP\""
    echo "export ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP=\"$CHANNEL_GROUP\""
    echo "export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION=\"$VERSION\""
    echo "export ARO_HCP_OPENSHIFT_NODEPOOL_VERSION=\"$VERSION\""
}

if main "$@"; then
    status=0
else
    status=$?
fi

# Restore shell options if sourced
if is_sourced; then
    eval "$OLD_SHELL_OPTS"
    return "$status"
else
    exit "$status"
fi
