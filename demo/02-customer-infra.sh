#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars

az group create --name "${CUSTOMER_RG_NAME}" --location ${LOCATION} --tags persist=true

az network nsg create --resource-group ${CUSTOMER_RG_NAME} --name ${CUSTOMER_NSG}
NSG_ID=$(az network nsg list --query "[?name=='${CUSTOMER_NSG}'].id" --resource-group ${CUSTOMER_RG_NAME} --output tsv)

az network vnet create \
  --name ${CUSTOMER_VNET_NAME} \
  --resource-group ${CUSTOMER_RG_NAME} \
  --address-prefix 10.0.0.0/16 \
  --subnet-name ${CUSTOMER_VNET_SUBNET1} \
  --subnet-prefixes 10.0.0.0/24 \
  --nsg "${NSG_ID}"  --location ${LOCATION}

# Check if key vault exists, create only if it doesn't
if ! az keyvault show --name "${CUSTOMER_KV_NAME}" --resource-group "${CUSTOMER_RG_NAME}" >/dev/null 2>&1; then
  echo "Creating key vault ${CUSTOMER_KV_NAME}..."
  az keyvault create \
    --name "${CUSTOMER_KV_NAME}" \
    --resource-group "${CUSTOMER_RG_NAME}" \
    --location "${LOCATION}" \
    --enable-rbac-authorization true
else
  echo "Key vault ${CUSTOMER_KV_NAME} already exists, skipping creation."
fi

# use the ARM apis instead of dataplane for key vault (az keyvault key create)
az rest --method PUT --uri "/subscriptions/${SUBSCRIPTION_ID}/resourcegroups/${CUSTOMER_RG_NAME}/providers/Microsoft.KeyVault/vaults/${CUSTOMER_KV_NAME}/keys/${ETCD_ENCRYPTION_KEY_NAME}?api-version=2024-12-01-preview" \
  --body '{
    "properties": {
      "keySize": 2048,
      "kty": "RSA"
    }
  }' --headers 'Content-Type=application/json'
