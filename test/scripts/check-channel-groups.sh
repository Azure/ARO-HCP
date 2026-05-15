#!/bin/bash
# Script to check and validate OpenShift channel group configuration

set -euo pipefail

echo "=== OpenShift Version Configuration Check ==="
echo ""

# Check environment variables
echo "Environment Variables:"
echo "  ARO_HCP_OPENSHIFT_CHANNEL_GROUP: ${ARO_HCP_OPENSHIFT_CHANNEL_GROUP:-<not set>}"
echo "  ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP: ${ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP:-<not set>}"
echo "  ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION: ${ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION:-<not set>}"
echo "  ARO_HCP_OPENSHIFT_NODEPOOL_VERSION: ${ARO_HCP_OPENSHIFT_NODEPOOL_VERSION:-<not set>}"
echo ""

# Determine effective channel groups
CP_CHANNEL="${ARO_HCP_OPENSHIFT_CHANNEL_GROUP:-candidate}"
NP_CHANNEL="${ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP:-candidate}"

echo "Effective Channel Groups:"
echo "  Control Plane: $CP_CHANNEL"
echo "  Node Pool: $NP_CHANNEL"
echo ""

# Check if they match
if [ "$CP_CHANNEL" = "$NP_CHANNEL" ]; then
    echo "✓ Channel groups MATCH - versions should be consistent"
else
    echo "✗ WARNING: Channel groups DIFFER - this may cause version mismatches!"
    echo "  Recommendation: Set both to the same channel group"
fi
echo ""

# Check explicit version overrides
if [ -n "${ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION:-}" ] || [ -n "${ARO_HCP_OPENSHIFT_NODEPOOL_VERSION:-}" ]; then
    echo "Explicit Version Overrides Detected:"
    if [ -n "${ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION:-}" ]; then
        echo "  Control Plane: $ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION"
    fi
    if [ -n "${ARO_HCP_OPENSHIFT_NODEPOOL_VERSION:-}" ]; then
        echo "  Node Pool: $ARO_HCP_OPENSHIFT_NODEPOOL_VERSION"
    fi

    if [ "${ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION:-unset}" != "${ARO_HCP_OPENSHIFT_NODEPOOL_VERSION:-unset}" ]; then
        echo "  ✗ WARNING: Versions are different!"
        echo "  This WILL cause validation errors:"
        echo "  'Node pool version must not be greater than Control Plane version'"
    fi
fi

echo ""
echo "=== Recommendations ==="
echo "1. Ensure ARO_HCP_OPENSHIFT_CHANNEL_GROUP == ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP"
echo "2. If setting versions explicitly, use the SAME version for both"
echo "3. For CI/CD, use a single version resolution step and pass it to both"
