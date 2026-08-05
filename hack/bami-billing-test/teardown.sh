#!/bin/bash
# Delete the cluster, then the customer resource group. See README.md.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
source "${SCRIPT_DIR}/lib.sh"

echo "==> Deleting cluster ${CLUSTER_NAME} (this also removes its node pools and managed RG)"
# Delete the cluster first so the RP cleans up the managed RG before we remove
# the customer infra it depends on.
if az resource show --ids "${CLUSTER_RESOURCE_ID}" --api-version "${CLUSTER_API_VERSION}" >/dev/null 2>&1; then
  az resource delete --ids "${CLUSTER_RESOURCE_ID}" --api-version "${CLUSTER_API_VERSION}"
else
  echo "    Cluster not found; skipping."
fi

echo "==> Deleting customer resource group ${CUSTOMER_RG_NAME}"
az group delete \
  --name "${CUSTOMER_RG_NAME}" \
  --subscription "${SUBSCRIPTION}" \
  --yes

echo "==> Done."
