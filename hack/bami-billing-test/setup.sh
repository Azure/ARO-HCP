#!/bin/bash
# Deploy customer infra + HCP cluster + node pool for billing testing. See README.md.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
source "${SCRIPT_DIR}/lib.sh"

print_config
echo
verify_provider_and_location
verify_afec_flags

az group create \
  --name "${CUSTOMER_RG_NAME}" \
  --subscription "${SUBSCRIPTION}" \
  --location "${LOCATION}" \
  --tags persist=false purpose=bami-billing-test

echo "==> Deploying customer infrastructure (vnet, subnets, nsg, keyvault)"
az deployment group create \
  --name 'bami-billing-infra' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file "${BICEP_DIR}/customer-infra.bicep" \
  --parameters \
    customerNsgName="${CUSTOMER_NSG}" \
    customerVnetName="${CUSTOMER_VNET_NAME}" \
    customerVnetSubnetName="${CUSTOMER_VNET_SUBNET1}" \
    customerVirtualNetworkIntegrationSubnetName="${CUSTOMER_VNET_INTEGRATION_SUBNET}" \
    privateKeyVault="${PRIVATE_KEYVAULT}"

KEYVAULT_NAME="$(az deployment group show \
  --name 'bami-billing-infra' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --query "properties.outputs.keyVaultName.value" -o tsv)"

echo "==> Deploying HCP cluster ${CLUSTER_NAME}"
az deployment group create \
  --name 'bami-billing-cluster' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file "${BICEP_DIR}/cluster.bicep" \
  --parameters \
    vnetName="${CUSTOMER_VNET_NAME}" \
    subnetName="${CUSTOMER_VNET_SUBNET1}" \
    vnetIntegrationSubnetName="${CUSTOMER_VNET_INTEGRATION_SUBNET}" \
    nsgName="${CUSTOMER_NSG}" \
    clusterName="${CLUSTER_NAME}" \
    managedResourceGroupName="${MANAGED_RESOURCE_GROUP}" \
    keyVaultName="${KEYVAULT_NAME}" \
    privateKeyVault="${PRIVATE_KEYVAULT}" \
    clusterVersion="${CLUSTER_VERSION}"

echo "==> Deploying node pool ${NP_NAME} (${NODEPOOL_REPLICAS} x ${NODEPOOL_VM_SIZE})"
echo "==> Deploying node pools"
i=0
for spec in ${NODEPOOL_SPECS}; do
  if [[ "${spec}" == *:* ]]; then
    vm="${spec%%:*}"; count="${spec##*:}"
  else
    vm="${spec}"; count=1
  fi
  np_name="$(nodepool_name_for_sku "${vm}")"
  echo "    ${np_name}: ${count} x ${vm}"
  az deployment group create \
    --name "bami-billing-nodepool-${i}" \
    --subscription "${SUBSCRIPTION}" \
    --resource-group "${CUSTOMER_RG_NAME}" \
    --template-file "${SCRIPT_DIR}/bicep/nodepool.bicep" \
    --parameters \
      clusterName="${CLUSTER_NAME}" \
      nodePoolName="${np_name}" \
      nodePoolVersion="${NODEPOOL_VERSION}" \
      vmSize="${vm}" \
      replicas="${count}" \
      osDiskSizeGiB="${NODEPOOL_OSDISK_GIB}" \
      osDiskStorageAccountType="${NODEPOOL_OSDISK_TYPE}"
  i=$((i + 1))
done

echo
echo "==> Done. Cluster resource ID:"
echo "    ${CLUSTER_RESOURCE_ID}"
echo "    Inspect with: ./show.sh"
echo "    Tear down with: ./teardown.sh"
