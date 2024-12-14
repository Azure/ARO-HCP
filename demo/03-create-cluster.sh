#!/bin/bash

source env_vars

CURRENT_DATE=$(date -u +"%Y-%m-%dT%H:%M:%S+00:00")

CLUSTER_TMPL_FILE="cluster.tmpl.json"
CLUSTER_FILE="cluster.json"

NSG_ID=$(az network nsg list -g ${CUSTOMER_RG_NAME} --query "[?name=='${CUSTOMER_NSG}'].id" -o tsv)
SUBNET_ID=$(az network vnet subnet show -g ${CUSTOMER_RG_NAME} --vnet-name ${CUSTOMER_VNET_NAME} --name ${CUSTOMER_VNET_SUBNET1} --query id -o tsv)
SUBSCRIPTION_ID=$(az account show --query id -o tsv)

jq \
  --arg managed_rg "$MANAGED_RESOURCE_GROUP" \
  --arg subnet_id "$SUBNET_ID" \
  --arg nsg_id "$NSG_ID" \
  '.properties.spec.platform.managedResourceGroup = $managed_rg | .properties.spec.platform.subnetId = $subnet_id | .properties.spec.platform.networkSecurityGroupId = $nsg_id' "${CLUSTER_TMPL_FILE}" > ${CLUSTER_FILE}

curl -i -X PUT "localhost:8443/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}?api-version=2024-06-10-preview" \
  --header "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"${USER}\", \"createdByType\": \"User\", \"createdAt\": \"${CURRENT_DATE}\"}" \
  --json @${CLUSTER_FILE}
