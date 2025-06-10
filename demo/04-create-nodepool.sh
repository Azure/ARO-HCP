#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

NODEPOOL_TMPL_FILE="node_pool.tmpl.json"
NODEPOOL_FILE="node_pool.json"

SUBNET_ID=$(az network vnet subnet show --resource-group ${CUSTOMER_RG_NAME} --vnet-name ${CUSTOMER_VNET_NAME} --name ${CUSTOMER_VNET_SUBNET1} --query id --output tsv)

jq \
  --arg location "$LOCATION" \
  --arg subnet_id "$SUBNET_ID" \
  '
    .location = $location |
    .properties.platform.subnetId = $subnet_id
  ' "${NODEPOOL_TMPL_FILE}" > ${NODEPOOL_FILE}

(arm_system_data_header; correlation_headers) | curl --silent --show-error --include --request PUT "localhost:8443${NODE_POOL_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}" \
  --header @- \
  --json @${NODEPOOL_FILE}
