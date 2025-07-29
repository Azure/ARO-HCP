#!/bin/bash
# Create an ARO HCP Cluster + Node pool using bicep.
set -o errexit
set -o nounset
set -o pipefail

source env_vars
# The ONLY supported region for ARO-HCP in INT is uksouth
LOCATION=uksouth
# This is the only supported subscription for creating INT/STAGE hcp/nodepools
SUBSCRIPTION=$(az account show --query 'name' -o tsv)

if [[ "${SUBSCRIPTION}" != "ARO HCP - STAGE testing (EA Subscription)" && "${SUBSCRIPTION}" != "ARO SRE Team - INT (EA Subscription 3)" ]]; then
  echo "ERROR: Current subscription is '${SUBSCRIPTION}'."
  echo "This script must be run in either:"
  echo "  - 'ARO HCP - STAGE testing (EA Subscription)' - for testing ARO HCP in STAGE (AME)"
  echo "  - 'ARO SRE Team - INT (EA Subscription 3)'    - for testing ARO HCP in INT (MSIT)"
  echo "Please run 'az account set --subscription <subscription-name>' to switch to the correct subscription"
  exit 1
fi

az group create \
  --name "${CUSTOMER_RG_NAME}" \
  --subscription "${SUBSCRIPTION}" \
  --location "${LOCATION}"

az deployment group create \
  --name 'infra' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file bicep/customer-infra.bicep \
  --parameters \
    customerNsgName="${CUSTOMER_NSG}" \
    customerVnetName="${CUSTOMER_VNET_NAME}" \
    customerVnetSubnetName="${CUSTOMER_VNET_SUBNET1}"

NSG_ID=$(az deployment group show \
          --name 'infra' \
          --subscription "${SUBSCRIPTION}" \
          --resource-group "${CUSTOMER_RG_NAME}" \
          --query "properties.outputs.networkSecurityGroupId.value" -o tsv)

SUBNET_ID=$(az deployment group show \
          --name 'infra' \
          --subscription "${SUBSCRIPTION}" \
          --resource-group "${CUSTOMER_RG_NAME}" \
          --query "properties.outputs.subnetId.value" -o tsv)

az deployment group create \
  --name 'aro-hcp'\
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file bicep/cluster.bicep \
  --parameters \
    vnetName="${CUSTOMER_VNET_NAME}" \
    subnetName="${CUSTOMER_VNET_SUBNET1}" \
    nsgName="${CUSTOMER_NSG}" \
    clusterName="${CLUSTER_NAME}" \
    managedResourceGroupName="${MANAGED_RESOURCE_GROUP}"

az deployment group create \
  --name 'node-pool' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file bicep/nodepool.bicep \
  --parameters \
    clusterName="${CLUSTER_NAME}" \
    nodePoolName="${NP_NAME}"
