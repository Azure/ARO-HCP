#!/bin/bash

#
# ARO HCP E2E Setup: create ARO HCP hosted cluster via RP API
#

set -o errexit
set -o nounset
set -o pipefail

source env.defaults

set -o xtrace

# TODO: can we avoid fetching IDs?
NSG_ID=$(az network nsg list -g "${CUSTOMER_RG_NAME}" --query "[?name=='${CUSTOMER_NSG_NAME}'].id" -o tsv)
SUBNET_ID=$(az network vnet subnet show -g "${CUSTOMER_RG_NAME}" --vnet-name "${CUSTOMER_VNET_NAME}" --name "${CUSTOMER_VNET_SUBNET1}" --query id -o tsv)

jq \
  --arg managed_rg "${MANAGED_RESOURCE_GROUP}" \
  --arg subnet_id "${SUBNET_ID}" \
  --arg nsg_id "${NSG_ID}" \
  --argjson uamis_json_map "$(cat "${UAMIS_JSON_FILENAME}")" \
  --argjson identity_uamis_json_map "$(cat "${IDENTITY_UAMIS_JSON_FILENAME}")" \
  '
    .properties.platform.managedResourceGroup = $managed_rg |
    .properties.platform.subnetId = $subnet_id |
    .properties.platform.networkSecurityGroupId = $nsg_id |
    .properties.platform.operatorsAuthentication.userAssignedIdentities = $uamis_json_map |
    .identity.userAssignedIdentities = $identity_uamis_json_map
  ' "${CLUSTER_TMPL_FILE}" > "${CLUSTER_JSON_FILENAME}"

jq '.' "${CLUSTER_JSON_FILENAME}"

AZURE_PATH="/subscriptions/${CUSTOMER_SUBSCRIPTION}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}?api-version=2024-06-10-preview"

./arocurl.sh -v -c \
  PUT "${AZURE_PATH}" \
  --json @"${CLUSTER_JSON_FILENAME}"

./aro-curl-wait.sh -t 1800 "${AZURE_PATH}" "Succeeded"
