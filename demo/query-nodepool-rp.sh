#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

SUBSCRIPTION_ID=$(az account show --query id -o tsv)

correlation_headers | curl -s -H @- "localhost:8443/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}/nodePools/${NP_NAME}?api-version=2024-06-10-preview" | jq
