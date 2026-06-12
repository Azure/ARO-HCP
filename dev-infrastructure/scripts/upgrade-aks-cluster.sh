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
        | tail -n1 || true)

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

if [ "${NEEDS_UPGRADE}" = "true" ]; then
    echo "Upgrading cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}' to '${TARGET_VERSION}'..."

    az aks upgrade \
        --resource-group "${RESOURCE_GROUP}" \
        --name "${CLUSTER_NAME}" \
        --kubernetes-version "${TARGET_VERSION}" \
        --yes

    echo "Waiting for Kubernetes version upgrade to complete..."
    az aks wait \
        --resource-group "${RESOURCE_GROUP}" \
        --name "${CLUSTER_NAME}" \
        --updated \
        --timeout 3600

    echo "Kubernetes version upgrade completed successfully."
else
    echo "Control plane and node pools are at or above target version ${TARGET_VERSION} - no version upgrade needed."
fi

# Node OS image upgrade.
#
# This is the pipeline-driven replacement for the AKS-managed
# nodeOSUpgradeChannel/auto-upgrade, which is disabled in the AKS base module
# (autoUpgradeProfile.nodeOSUpgradeChannel: 'None', see the companion change in
# dev-infrastructure/modules/aks-cluster-base.bicep). It used to pull the latest
# node image (security patches) on a maintenance schedule; we now drive it from
# the pipeline: for each node pool, compare the running node image against the
# latest available one and run a node-image-only upgrade when a newer image
# exists. A Kubernetes version upgrade above already reimages nodes to the
# latest image for the target version, so this is typically a no-op right after
# one and only does work when the image alone is stale.
echo "Checking node image versions..."
NODE_IMAGE_UPGRADE_NEEDED=false

NODE_POOL_NAMES=$(az aks nodepool list \
    --resource-group "${RESOURCE_GROUP}" \
    --cluster-name "${CLUSTER_NAME}" \
    --query '[].name' \
    --output tsv)

if [ -n "${NODE_POOL_NAMES}" ]; then
    while IFS= read -r POOL_NAME; do
        [ -z "${POOL_NAME}" ] && continue

        CURRENT_IMAGE=$(az aks nodepool show \
            --resource-group "${RESOURCE_GROUP}" \
            --cluster-name "${CLUSTER_NAME}" \
            --name "${POOL_NAME}" \
            --query nodeImageVersion \
            --output tsv)

        LATEST_IMAGE=$(az aks nodepool get-upgrades \
            --resource-group "${RESOURCE_GROUP}" \
            --cluster-name "${CLUSTER_NAME}" \
            --nodepool-name "${POOL_NAME}" \
            --query latestNodeImageVersion \
            --output tsv 2>/dev/null || true)

        echo "  Node pool '${POOL_NAME}': image ${CURRENT_IMAGE} (latest available: ${LATEST_IMAGE:-unknown})"

        if [ -n "${LATEST_IMAGE}" ] && [ "${CURRENT_IMAGE}" != "${LATEST_IMAGE}" ]; then
            echo "  Node pool '${POOL_NAME}' has a newer node image available"
            NODE_IMAGE_UPGRADE_NEEDED=true
        fi
    done <<< "${NODE_POOL_NAMES}"
fi

if [ "${NODE_IMAGE_UPGRADE_NEEDED}" = "true" ]; then
    echo "Upgrading node images for cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}'..."

    az aks upgrade \
        --resource-group "${RESOURCE_GROUP}" \
        --name "${CLUSTER_NAME}" \
        --node-image-only \
        --yes

    echo "Waiting for node image upgrade to complete..."
    az aks wait \
        --resource-group "${RESOURCE_GROUP}" \
        --name "${CLUSTER_NAME}" \
        --updated \
        --timeout 3600

    echo "Node image upgrade completed successfully."
else
    echo "Node images are up to date - no node image upgrade needed."
fi
