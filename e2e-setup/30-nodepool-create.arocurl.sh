#!/bin/bash

#
# ARO HCP E2E Setup: create ARO HCP nodepool
#

set -o errexit
set -o nounset
set -o pipefail

source env.defaults
export NODEPOOL_JSON_FILENAME=${CLUSTER_NAME}.${NODEPOOL_NAME}.nodepool.json

set -o xtrace

# TODO: can we avoid fetching IDs?
SUBNET_ID=$(az network vnet subnet show -g "${CUSTOMER_RG_NAME}" --vnet-name "${CUSTOMER_VNET_NAME}" --name "${CUSTOMER_VNET_SUBNET1}" --query id -o tsv)

jq \
  --arg subnet_id "$SUBNET_ID" \
  '.properties.platform.subnetId = $subnet_id' "${NODEPOOL_TMPL_FILE}" > "${NODEPOOL_JSON_FILENAME}"

jq '.' "${NODEPOOL_JSON_FILENAME}"

AZURE_PATH="/subscriptions/${CUSTOMER_SUBSCRIPTION}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}/nodePools/${NODEPOOL_NAME}?api-version=2024-06-10-preview"

./arocurl.sh -v -c \
  PUT "${AZURE_PATH}" \
  --json @"${NODEPOOL_JSON_FILENAME}"

./aro-curl-wait.sh -t 1800 "${AZURE_PATH}" "Succeeded"
