#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

SUBSCRIPTION_ID=$(az account show --query id -o tsv)

# The last node pool can not be deleted from a cluster. See https://issues.redhat.com/browse/XCMSTRAT-1069 for more details.

(arm_system_data_header; correlation_headers) | curl -sSi -X DELETE "localhost:8443/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}/nodePools/${NP_NAME}?api-version=2024-06-10-preview" \
  --header @-
