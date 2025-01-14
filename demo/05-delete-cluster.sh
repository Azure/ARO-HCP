#!/bin/bash

set -e

source env_vars
source "$(dirname "$0")"/common.sh

get_existing_cluster_payload() {
  EXISTING_CLUSTER_PAYLOAD=$(curl -s "${FRONTEND_HOST}:${FRONTEND_PORT}${CLUSTER_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}")
}

delete_managed_identities_from_cluster() {
  UAMIS_JSON_MAP=$(echo ${EXISTING_CLUSTER_PAYLOAD} | jq '.properties.spec.platform.operatorsAuthentication.userAssignedIdentities')

  CP_UAMIS_ENTRIES=$(echo ${UAMIS_JSON_MAP}| jq -c '.controlPlaneOperators | to_entries | .[]')
  echo -n "${CP_UAMIS_ENTRIES}" | while read cp_uami_entry; do
    cp_operator_name=$(echo -n "${cp_uami_entry}" | jq .key)
    cp_operator_mi=$(echo -n "${cp_uami_entry}" | jq .value)
    echo "deleting $cp_operator_name operator's managed identity $cp_operator_mi"
    az identity delete --ids $cp_operator_mi
    echo "deleted managed identity $cp_operator_mi"
  done

  DP_UAMIS_ENTRIES=$(echo ${UAMIS_JSON_MAP} | jq -c '.dataPlaneOperators | to_entries | .[]')
  echo -n "${DP_UAMIS_ENTRIES}" | while read dp_uami_entry; do
    dp_operator_name=$(echo -n "${dp_uami_entry}" | jq .key)
    dp_operator_mi=$(echo -n "${dp_uami_entry}" | jq .value)
    echo "deleting $dp_operator_name operator's managed identity $dp_operator_mi"
    az identity delete --ids $dp_operator_mi
    echo "deleted managed identity $dp_operator_mi"
  done

  SMI_UAMI_ENTRY=$(echo ${UAMIS_JSON_MAP} | jq .serviceManagedIdentity)
  echo "deleting service managed identity ${SMI_UAMI_ENTRY}"
  az identity delete --ids ${SMI_UAMI_ENTRY}
  echo "deleted service managed identity ${SMI_UAMI_ENTRY}"
}

delete_cluster() {
  echo "deleting cluster ${CLUSTER_RESOURCE_ID}"
  correlation_headers | curl -si -H @- -X DELETE "${FRONTEND_HOST}:${FRONTEND_PORT}${CLUSTER_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}"
  echo "deleted cluster ${CLUSTER_RESOURCE_ID}"
}

main() {
  SUBSCRIPTION_ID=$(az account show --query id -o tsv)

  CLUSTER_RESOURCE_ID="/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}"
  FRONTEND_API_VERSION_QUERY_PARAM="api-version=2024-06-10-preview"
  FRONTEND_HOST="localhost"
  FRONTEND_PORT="8443"

  EXISTING_CLUSTER_PAYLOAD=""
  get_existing_cluster_payload
  if [ -z "${EXISTING_CLUSTER_PAYLOAD}" ]; then
    echo "cluster with resource id ${CLUSTER_RESOURCE_ID} not found"
    exit 0
  fi

  delete_cluster
  delete_managed_identities_from_cluster
}

main "$@"
