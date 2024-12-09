#!/bin/bash

source env_vars

CURRENT_DATE=$(date -u +"%Y-%m-%dT%H:%M:%S+00:00")

NODEPOOL_TMPL_FILE="node_pool.tmpl.json"
NODEPOOL_FILE="node_pool.json"

SUBNET_ID=$(az network vnet subnet show -g ${CUSTOMER_RG_NAME} --vnet-name ${CUSTOMER_VNET_NAME} --name ${CUSTOMER_VNET_SUBNET1} --query id -o tsv)
SUBSCRIPTION_ID=$(az account show --query id -o tsv)

jq \
  --arg managed_rg "$MANAGED_RESOURCE_GROUP" \
  --arg subnet_id "$SUBNET_ID" \
  --arg nsg_id "$NSG_ID" \
  '.properties.spec.platform.subnetId = $subnet_id' "${NODEPOOL_TMPL_FILE}" > ${NODEPOOL_FILE}


curl -i -X PUT "localhost:8443/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}/nodePools/${NP_NAME}?api-version=2024-06-10-preview" \
  --header "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"${USER}\", \"createdByType\": \"User\", \"createdAt\": \"${CURRENT_DATE}\"}" \
  --json @${NODEPOOL_FILE}
