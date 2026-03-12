#!/bin/bash
set -euo pipefail

# Inputs via environment variables:
#   CLUSTER_NAME   - AKS cluster name
#   RESOURCE_GROUP - Resource group containing the cluster
#   POOL_NAME_PATTERN - Regex pattern to match nodepool names (e.g. "user[0-9]+")

echo "Listing user nodepools matching '${POOL_NAME_PATTERN}' on cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}'..."

ALL_POOLS=$(az aks nodepool list \
  --cluster-name "${CLUSTER_NAME}" \
  --resource-group "${RESOURCE_GROUP}" \
  --query "[?mode=='User'].name" \
  --output tsv)

POOLS=$(echo "${ALL_POOLS}" | grep -E "^${POOL_NAME_PATTERN}$" | xargs)

if [ -z "${POOLS}" ]; then
  echo "No user nodepools found matching '${POOL_NAME_PATTERN}'. Nothing to do."
  exit 0
fi

echo "Found pools: ${POOLS}"

ERRORS=0

for POOL in ${POOLS}; do
  echo ""
  echo "=== Processing pool '${POOL}' ==="

  NODES=$(kubectl get nodes -l "agentpool=${POOL}" -o name 2>/dev/null || true)

  DRAIN_FAILED=false
  if [ -n "${NODES}" ]; then
    echo "Draining pool '${POOL}'"
    for NODE in ${NODES}; do
      echo "  Draining ${NODE}..."
      kubectl cordon "${NODE}"
      if ! kubectl drain "${NODE}" \
        --ignore-daemonsets \
        --delete-emptydir-data \
        --timeout=300s; then
        echo "  ERROR: Failed to drain ${NODE}"
        DRAIN_FAILED=true
        ERRORS=$((ERRORS + 1))
      fi
    done
  else
    echo "No nodes found in pool, skipping cordon/drain."
  fi

  if [ "${DRAIN_FAILED}" = true ]; then
    echo "Skipping deletion of pool '${POOL}' due to drain failures."
    continue
  fi

  echo "Deleting pool '${POOL}'..."
  az aks nodepool delete \
    --cluster-name "${CLUSTER_NAME}" \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${POOL}" \
    --output none

  echo "Pool '${POOL}' deleted."
done

echo ""
if [ "${ERRORS}" -gt 0 ]; then
  echo "Finished with ${ERRORS} drain error(s). Some pools were not deleted."
  exit 1
fi
echo "All matching user nodepools have been processed."
