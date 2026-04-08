#!/bin/bash
# Create an ARO HCP Cluster + Node pool using bicep.
set -o errexit
set -o nounset
set -o pipefail

source env_vars
LOCATION=${LOCATION:-uksouth}
SUBSCRIPTION=$(az account show --query 'name' -o tsv)

PROVIDER_JSON=$(az provider show --namespace Microsoft.RedHatOpenShift -o json)
if [[ "Registered" != "$(echo ${PROVIDER_JSON} | jq -r .registrationState)" ]]; then
  echo "ERROR: Microsoft.RedHatOpenShift provider is not registered."
  exit 1
fi

# make sure location is supported for the subscription
if [[ -z $(echo $PROVIDER_JSON | jq --arg location "${LOCATION}" -r '.resourceTypes[] | select(.resourceType | ascii_downcase == "hcpopenshiftclusters") | .locations[] | select(. | ascii_downcase | gsub(" "; "") == $location)') ]]; then
  echo "ERROR: Location '${LOCATION}' is not supported for the Microsoft.RedHatOpenShift/hcpopenshiftclusters resource type."
  exit 1
fi

PRIVATE_KEYVAULT=true
CLUSTER_VERSION="4.20"
NODEPOOL_VERSION="4.20.16"


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
    customerVnetSubnetName="${CUSTOMER_VNET_SUBNET1}" \
    customerVirtualNetworkIntegrationSubnetName="${CUSTOMER_VNET_INTEGRATION_SUBNET}" \
    privateKeyVault=${PRIVATE_KEYVAULT}

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

KEYVAULT_NAME=$(az deployment group show \
          --name 'infra' \
          --subscription "${SUBSCRIPTION}" \
          --resource-group "${CUSTOMER_RG_NAME}" \
          --query "properties.outputs.keyVaultName.value" -o tsv)

VNET_INTEGRATION_SUBNET_ID=$(az deployment group show \
          --name 'infra' \
          --subscription "${SUBSCRIPTION}" \
          --resource-group "${CUSTOMER_RG_NAME}" \
          --query "properties.outputs.vnetIntegrationSubnetId.value" -o tsv)

az deployment group create \
  --name 'aro-hcp'\
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file bicep/cluster.bicep \
  --parameters \
    vnetName="${CUSTOMER_VNET_NAME}" \
    subnetName="${CUSTOMER_VNET_SUBNET1}" \
    vnetIntegrationSubnetName="${CUSTOMER_VNET_INTEGRATION_SUBNET}" \
    nsgName="${CUSTOMER_NSG}" \
    clusterName="${CLUSTER_NAME}" \
    managedResourceGroupName="${MANAGED_RESOURCE_GROUP}" \
    keyVaultName="${KEYVAULT_NAME}" \
    privateKeyVault=${PRIVATE_KEYVAULT} \
    clusterVersion="${CLUSTER_VERSION}"

az deployment group create \
  --name 'node-pool' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file bicep/nodepool.bicep \
  --parameters \
    clusterName="${CLUSTER_NAME}" \
    nodePoolName="${NP_NAME}" \
    nodePoolVersion="${NODEPOOL_VERSION}"
