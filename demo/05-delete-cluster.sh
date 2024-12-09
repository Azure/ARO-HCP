#!/bin/bash

source env_vars
SUBSCRIPTION_ID=$(az account show --query id -o tsv)

curl -i -X DELETE "localhost:8443/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}?api-version=2024-06-10-preview"
