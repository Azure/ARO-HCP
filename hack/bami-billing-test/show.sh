#!/bin/bash
# Print cluster + node pool provisioning state (billing starts at Succeeded).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
source "${SCRIPT_DIR}/lib.sh"

print_config
echo

echo "==> Cluster"
az resource show \
  --ids "${CLUSTER_RESOURCE_ID}" \
  --api-version "${CLUSTER_API_VERSION}" \
  --query "{name:name, provisioningState:properties.provisioningState, location:location}" \
  -o jsonc || echo "    (cluster not found)"

echo "==> Node pools"
for spec in ${NODEPOOL_SPECS}; do
  vm="${spec%%:*}"
  np_name="$(nodepool_name_for_sku "${vm}")"
  echo "-- ${np_name} (${vm})"
  az resource show \
    --ids "${CLUSTER_RESOURCE_ID}/nodePools/${np_name}" \
    --api-version "${CLUSTER_API_VERSION}" \
    --query "{name:name, provisioningState:properties.provisioningState, vmSize:properties.platform.vmSize, replicas:properties.replicas}" \
    -o jsonc || echo "    (node pool not found)"
done
