#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars

az group create --name "${CUSTOMER_RG_NAME}" --location ${LOCATION}

az network nsg create -g ${CUSTOMER_RG_NAME} --name ${CUSTOMER_NSG}
NSG_ID=$(az network nsg list --query "[?name=='${CUSTOMER_NSG}'].id" -g ${CUSTOMER_RG_NAME} -o tsv)

az network vnet create \
  --name ${CUSTOMER_VNET_NAME} \
  --resource-group ${CUSTOMER_RG_NAME} \
  --address-prefix 10.0.0.0/16 \
  --subnet-name ${CUSTOMER_VNET_SUBNET1} \
  --subnet-prefixes 10.0.0.0/24 \
  --nsg "${NSG_ID}"  --location ${LOCATION}
