#!/bin/bash
set -euo pipefail

# Inputs via environment variables:
#   RESOURCE_GROUP - Resource group containing the cluster
#   CLUSTER_NAME   - AKS cluster name
#   KUBERNETES_VERSION - Kubernetes Version. May be a minor version (e.g. "1.35")
#                        or a pinned patch version (e.g. "1.35.5").

version_greater_than() {
    [ "$(printf '%s\n' "$1" "$2" | sort -V | head -n1)" != "$1" ]
}

# Resolve the requested Kubernetes version to a concrete upgrade target.
#
# When KUBERNETES_VERSION is a minor version (X.Y), AKS would otherwise pick an
# arbitrary default patch on upgrade and never chase newer patches once the
# cluster is at or above that minor. Instead, resolve it to the highest patch
# (X.Y.Z) currently available for that minor in the cluster's region, taking
# into account both the current version and the patches AKS offers as upgrades.
#
# When a full patch version (X.Y.Z) is pinned, honor it verbatim.
resolve_target_version() {
    local requested="$1"
    local current="$2"

    if [[ "${requested}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "${requested}"
        return
    fi

    local available
    available=$(az aks get-upgrades \
        --resource-group "${RESOURCE_GROUP}" \
        --name "${CLUSTER_NAME}" \
        --query "controlPlaneProfile.upgrades[].kubernetesVersion" \
        --output tsv 2>/dev/null || true)

    local resolved
    resolved=$(printf '%s\n%s\n' "${current}" "${available}" \
        | grep -E "^${requested//./\\.}\.[0-9]+$" \
        | sort -V \
        | tail -n1)

    if [ -n "${resolved}" ]; then
        echo "${resolved}"
    else
        # No patch for the requested minor is available yet (or get-upgrades
        # failed); fall back to the requested minor and let AKS pick a patch.
        echo "${requested}"
    fi
}

echo "Checking if cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}' needs upgrade to '${KUBERNETES_VERSION}'..."

# Get current control plane version
CURRENT_CP_VERSION=$(az aks show \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --query currentKubernetesVersion \
    --output tsv)

TARGET_VERSION=$(resolve_target_version "${KUBERNETES_VERSION}" "${CURRENT_CP_VERSION}")

echo "Current control plane version: ${CURRENT_CP_VERSION}"
echo "Requested version: ${KUBERNETES_VERSION}"
echo "Resolved target version: ${TARGET_VERSION}"

# Check if control plane needs upgrade
NEEDS_UPGRADE=false
if version_greater_than "${TARGET_VERSION}" "${CURRENT_CP_VERSION}"; then
    echo "Control plane needs upgrade from ${CURRENT_CP_VERSION} to ${TARGET_VERSION}"
    NEEDS_UPGRADE=true
else
    echo "Control plane is at ${CURRENT_CP_VERSION}, target is ${TARGET_VERSION} - no upgrade needed"
fi

# Get node pool versions and check if any need upgrade
echo "Checking node pool versions..."
NODE_POOLS=$(az aks nodepool list \
    --resource-group "${RESOURCE_GROUP}" \
    --cluster-name "${CLUSTER_NAME}" \
    --query '[].{name:name,version:orchestratorVersion}' \
    --output tsv)

if [ -n "${NODE_POOLS}" ]; then
    while IFS=$'\t' read -r POOL_NAME POOL_VERSION; do
        echo "  Node pool '${POOL_NAME}': ${POOL_VERSION}"

        if version_greater_than "${TARGET_VERSION}" "${POOL_VERSION}"; then
            echo "  Node pool '${POOL_NAME}' needs upgrade from ${POOL_VERSION} to ${TARGET_VERSION}"
            NEEDS_UPGRADE=true
        else
            echo "  Node pool '${POOL_NAME}' is at ${POOL_VERSION}, target is ${TARGET_VERSION} - no upgrade needed"
        fi
    done <<< "${NODE_POOLS}"
fi

if [ "${NEEDS_UPGRADE}" = "false" ]; then
    echo "Cluster does not need upgrade - all components are at or above target version ${TARGET_VERSION}."
    exit 0
fi

echo "Upgrading cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}' to '${TARGET_VERSION}'..."

az aks upgrade \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --kubernetes-version "${TARGET_VERSION}" \
    --yes

echo "Waiting for upgrade to complete..."
az aks wait \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --updated \
    --timeout 3600

echo "Upgrade completed successfully."
