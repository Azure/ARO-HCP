#!/bin/bash

#
# ARO HCP E2E Setup: customer infrastrucure required for ARO HCP Cluster
#

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

#
# Resource group
#

az group create --name "${CUSTOMER_RG_NAME}" --location "${LOCATION}"

#
# Network
#

az network vnet create \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --name "${CUSTOMER_VNET_NAME}" \
  --address-prefixes 10.0.0.0/16

az network vnet subnet create \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --vnet-name "${CUSTOMER_VNET_NAME}" \
  --name "${CUSTOMER_VNET_SUBNET1}" \
  --address-prefixes 10.0.0.0/24

#
# Network Security Group
#

az network nsg create \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --name "${CUSTOMER_NSG_NAME}"

az network vnet subnet update \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --vnet-name "${CUSTOMER_VNET_NAME}" \
  --name "${CUSTOMER_VNET_SUBNET1}" \
  --network-security-group "${CUSTOMER_NSG_NAME}"

az network asg create \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --name aro-asg \
  --location "${LOCATION}"

az network nsg rule create \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --nsg-name "${CUSTOMER_NSG_NAME}" \
  --name Allow-Web-All \
  --access Allow \
  --protocol Tcp \
  --direction Inbound \
  --priority 100 \
  --source-address-prefix Internet \
  --source-port-range "*" \
  --destination-asgs "aro-asg" \
  --destination-port-range 443
