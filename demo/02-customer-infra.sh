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

az keyvault create \
  --name "${CUSTOMER_KV_NAME}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --location "${LOCATION}" \
  --enable-rbac-authorization true

# Get current user's object ID
CURRENT_USER_ID=$(az ad signed-in-user show -o json | jq -r '.id')

# Assign Key Vault Crypto Officer role to current user
az role assignment create \
  --assignee "${CURRENT_USER_ID}" \
  --role "Key Vault Crypto Officer" \
  --scope "/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.KeyVault/vaults/${CUSTOMER_KV_NAME}"

# Wait for role assignment to propagate with timeout
echo "Waiting for RBAC role assignment to propagate..."
max_retries=100
counter=0
while ! az keyvault key list --vault-name "${CUSTOMER_KV_NAME}" &>/dev/null; do
    if [ $counter -eq $max_retries ]; then
        echo "Timeout waiting for RBAC role assignment to propagate"
        exit 1
    fi
    echo "Waiting for RBAC permissions... (attempt $((counter + 1))/$max_retries)"
    counter=$((counter + 1))
    sleep 3
done

# Create RSA key in Key Vault
az keyvault key create \
  --vault-name "${CUSTOMER_KV_NAME}" \
  --name "etcd-data-kms-encryption-key" \
  --kty RSA \
  --size 2048
