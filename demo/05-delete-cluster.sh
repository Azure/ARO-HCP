#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

get_existing_cluster_payload() {
  EXISTING_CLUSTER_PAYLOAD=$(curl -sS "${FRONTEND_HOST}:${FRONTEND_PORT}${CLUSTER_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}")
}

delete_managed_identities_from_cluster() {
  UAMIS_JSON_MAP=$(echo ${EXISTING_CLUSTER_PAYLOAD} | jq '.properties.spec.platform.operatorsAuthentication.userAssignedIdentities')

  CP_UAMIS_ENTRIES=$(echo ${UAMIS_JSON_MAP}| jq -c '.controlPlaneOperators | to_entries | .[]')
  while read cp_uami_entry; do
    cp_operator_name=$(echo -n "${cp_uami_entry}" | jq .key)
    cp_operator_mi=$(echo -n "${cp_uami_entry}" | jq .value -r)
    echo "deleting $cp_operator_name operator's managed identity $cp_operator_mi"
    az identity delete --ids $cp_operator_mi
    echo "deleted managed identity $cp_operator_mi"
  done <<< "${CP_UAMIS_ENTRIES}"

  DP_UAMIS_ENTRIES=$(echo ${UAMIS_JSON_MAP} | jq -c '.dataPlaneOperators | to_entries | .[]')
  while read dp_uami_entry; do
    dp_operator_name=$(echo -n "${dp_uami_entry}" | jq .key)
    dp_operator_mi=$(echo -n "${dp_uami_entry}" | jq .value -r)
    echo "deleting $dp_operator_name operator's managed identity $dp_operator_mi"
    az identity delete --ids $dp_operator_mi
    echo "deleted managed identity $dp_operator_mi"
  done <<< "${DP_UAMIS_ENTRIES}"

  SMI_UAMI_ENTRY=$(echo ${UAMIS_JSON_MAP} | jq .serviceManagedIdentity -r)
  echo "deleting service managed identity ${SMI_UAMI_ENTRY}"
  az identity delete --ids ${SMI_UAMI_ENTRY}
  echo "deleted service managed identity ${SMI_UAMI_ENTRY}"
}

delete_cluster() {
  echo "deleting cluster ${CLUSTER_RESOURCE_ID}"
  correlation_headers | curl -sSi -H @- -X DELETE "${FRONTEND_HOST}:${FRONTEND_PORT}${CLUSTER_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}"
  if [ "${WAIT_FOR_CLUSTER_DELETION}" -eq "0" ]; then
    echo "wait for cluster deletion disabled. Continuing"
    return
  fi

  echo "waiting for cluster to be fully deleted ..."
  SLEEP_DURATION_SECONDS=10
  while true ; do
    CLUSTER_GET_RESP=$(curl -sS "${FRONTEND_HOST}:${FRONTEND_PORT}${CLUSTER_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}")
    CLUSTER_GET_RESP_PAYLOAD=$(echo ${CLUSTER_GET_RESP} | jq -r .)
    if [ "$?" -ne "0" ]; then
      echo "HTTP GET ${CLUSTER_RESOURCE_ID} returned invalid json:"
      echo "${CLUSTER_GET_RESP_PAYLOAD}"
      exit 1
    fi
    RESP_ID_ATTR=$(echo ${CLUSTER_GET_RESP_PAYLOAD} | jq -r '. | .id')
    if [ "${RESP_ID_ATTR}" == "null" ]; then
      RESP_ERR_CODE_ATTR=$(echo ${CLUSTER_GET_RESP_PAYLOAD} | jq -r '.error.code')
      if [ "${RESP_ERR_CODE_ATTR}" == "ResourceNotFound" ]; then
        # Cluster has been fully deleted so we return
        echo "deleted cluster ${CLUSTER_RESOURCE_ID}"
        return
      else
        echo "unexpected response when performing HTTP GET ${CLUSTER_RESOURCE_ID}":
        echo "${CLUSTER_GET_RESP_PAYLOAD}"
        exit 1
      fi
    fi

    if [ "${RESP_ID_ATTR}" != "${CLUSTER_RESOURCE_ID}" ]; then
        echo "unexpected cluster resource id when performing HTTP GET ${CLUSTER_RESOURCE_ID}":
        echo "${RESP_ID_ATTR}"
        exit 1
    fi

    echo "cluster not fully deleted yet. waiting for ${SLEEP_DURATION_SECONDS} seconds ..."
    sleep ${SLEEP_DURATION_SECONDS}

  done
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

  # WAIT_FOR_CLUSTER_DELETION controls whether the script
  # should wait until the cluster is fully deleted before
  # continuing with the managed identities deletion.
  # By default it is set to 1, which signals
  # that the script should wait until the cluster is
  # fully deleted before continuing.
  # To disable the wait set it to 0. However, this means
  # that the managed identities will be deleted while the
  # cluster is being deleted but still exists, which can
  # cause unexpected behavior / consequences, so use
  # with caution.
  WAIT_FOR_CLUSTER_DELETION=${WAIT_FOR_CLUSTER_DELETION:=1}

  delete_cluster
  delete_managed_identities_from_cluster
}

main "$@"
