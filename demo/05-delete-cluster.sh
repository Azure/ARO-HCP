#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

delete_managed_identities_from_cluster() {
  UAMIS_JSON_MAP=$(echo ${EXISTING_CLUSTER_PAYLOAD} | jq '.properties.platform.operatorsAuthentication.userAssignedIdentities')

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

main() {
  EXISTING_CLUSTER_PAYLOAD=$(rp_get_request "${CLUSTER_RESOURCE_ID}")
  if [ -z "${EXISTING_CLUSTER_PAYLOAD}" ]; then
    echo "cluster with resource id ${CLUSTER_RESOURCE_ID} not found"
    exit 0
  fi

  echo "deleting cluster ${CLUSTER_RESOURCE_ID}"
  rp_delete_request "${CLUSTER_RESOURCE_ID}"

  delete_managed_identities_from_cluster
}

main "$@"
