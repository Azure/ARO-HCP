#!/bin/bash

#
# ARO HCP E2E Setup: Managed Identities
#

# This is a temporary workaround for missing setup of managed identities in
# azure-cli aro hcp module (or elsewhere?) TODO: reference JIRA.
# For this reason, it doesn't follow all setup rules.

set -o errexit
set -o nounset
set -o pipefail

source env.defaults

#
# Constants and Variables
#

UAMIS_RESOURCE_IDS_PREFIX="/subscriptions/${CUSTOMER_SUBSCRIPTION}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.ManagedIdentity/userAssignedIdentities"

# A suffix that will be appended to all the
# user-assigned managed identities names that
# will be created for the cluster.
# By default we generate a 6 characters random
# suffix for the azure user-assigned managed
# identites to be used by the cluster to be created.
# The default can be overwritten by providing the
# environment variable OPERATORS_UAMIS_SUFFIX
# when running the script.
: OPERATORS_UAMIS_SUFFIX="${OPERATORS_UAMIS_SUFFIX:=$(openssl rand -hex 3)}"

# The service managed identity user assigned identity.
service_managed_identity_uami_name="${CLUSTER_NAME}-service-managed-identity-${OPERATORS_UAMIS_SUFFIX}"

# control plane operator names required for OCP 4.18.
# TODO in the future the information of the required
# identities for a given OCP version will be provided
# via API.
CONTROL_PLANE_OPERATORS_NAMES=(
  "cluster-api-azure"
  "control-plane"
  "cloud-controller-manager"
  "ingress"
  "disk-csi-driver"
  "file-csi-driver"
  "image-registry"
  "cloud-network-config"
  "kms"
)

# data plane operator names required for OCP 4.18.
# TODO in the future the information of the required
# identities for a given OCP version will be provided
# via API.
DATA_PLANE_OPERATORS_NAMES=(
  "disk-csi-driver"
  "file-csi-driver"
  "image-registry"
)

#
# Helper Functions
#

initialize_uamis_json_map()
{
  UAMIS_JSON_MAP='
  {
    "controlPlaneOperators": {},
    "dataPlaneOperators": {},
    "serviceManagedIdentity": ""
  }'

  service_managed_identity_uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${service_managed_identity_uami_name}"
  UAMIS_JSON_MAP=$(echo -n "${UAMIS_JSON_MAP}" | jq \
    --arg service_managed_identity_resource_id "$service_managed_identity_uami_resource_id" \
    '
      .serviceManagedIdentity = $service_managed_identity_resource_id
    '
  )

  for i in "${!CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[@]}"; do
    curr_operator_name="${CONTROL_PLANE_OPERATORS_NAMES[$i]}"
    curr_uami_name="${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
    curr_uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${curr_uami_name}"
    UAMIS_JSON_MAP=$(echo -n "${UAMIS_JSON_MAP}" | jq \
      --arg operator_name "$curr_operator_name" \
      --arg uami_resource_id "$curr_uami_resource_id" \
      '
        .controlPlaneOperators[$operator_name] = $uami_resource_id
      '
    )
  done

  for i in "${!DATA_PLANE_IDENTITIES_UAMIS_NAMES[@]}"; do
    curr_operator_name="${DATA_PLANE_OPERATORS_NAMES[$i]}"
    curr_uami_name="${DATA_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
    curr_uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${curr_uami_name}"
    UAMIS_JSON_MAP=$(echo -n "${UAMIS_JSON_MAP}" | jq \
      --arg operator_name "$curr_operator_name" \
      --arg uami_resource_id "$curr_uami_resource_id" \
      '
        .dataPlaneOperators[$operator_name] = $uami_resource_id
      '
    )
  done

}

initialize_uamis_identity_json_map()
{
  IDENTITY_UAMIS_JSON_MAP='{}'

  for i in "${!CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[@]}"; do
    curr_operator_name="${CONTROL_PLANE_OPERATORS_NAMES[$i]}"
    curr_uami_name="${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
    curr_uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${curr_uami_name}"
    IDENTITY_UAMIS_JSON_MAP=$(echo -n "${IDENTITY_UAMIS_JSON_MAP}" | jq \
      --arg operator_name "$curr_operator_name" \
      --arg uami_resource_id "$curr_uami_resource_id" \
      '
        .[$uami_resource_id] = {}
      '
    )
  done

  service_managed_identity_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${service_managed_identity_uami_name}"
  IDENTITY_UAMIS_JSON_MAP=$(echo -n "${IDENTITY_UAMIS_JSON_MAP}" | jq \
    --arg uami_resource_id "$service_managed_identity_resource_id" \
    '
      .[$uami_resource_id] = {}
    '
  )
}

#
# Initialize arrays with managed identity names
#

# We declare and initialize the control plane user assigned identities names
CONTROL_PLANE_IDENTITIES_UAMIS_NAMES=()
for operator_name in "${CONTROL_PLANE_OPERATORS_NAMES[@]}"; do
  CONTROL_PLANE_IDENTITIES_UAMIS_NAMES+=("${CLUSTER_NAME}-cp-${operator_name}-${OPERATORS_UAMIS_SUFFIX}")
done

# We declare and initialize the data plane user assigned identities names
DATA_PLANE_IDENTITIES_UAMIS_NAMES=()
for operator_name in "${DATA_PLANE_OPERATORS_NAMES[@]}"; do
  DATA_PLANE_IDENTITIES_UAMIS_NAMES+=("${CLUSTER_NAME}-dp-${operator_name}-${OPERATORS_UAMIS_SUFFIX}")
done

#
# Initialize json uamis variables
#

UAMIS_JSON_MAP=""
initialize_uamis_json_map


IDENTITY_UAMIS_JSON_MAP=""
initialize_uamis_identity_json_map

#
# Validate (via jq) and save json variables
#

set -o xtrace

jq '.' <<< "${UAMIS_JSON_MAP}" | tee "${UAMIS_JSON_FILENAME}"
jq '.' <<< "${IDENTITY_UAMIS_JSON_MAP}" | tee "${IDENTITY_UAMIS_JSON_FILENAME}"

#
# Create azure managed identities for the cluster
#

for uami_name in "${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[@]}"; do
  az identity create -n "${uami_name}" -g "${CUSTOMER_RG_NAME}"
done

for uami_name in "${DATA_PLANE_IDENTITIES_UAMIS_NAMES[@]}"; do
  az identity create -n "${uami_name}" -g "${CUSTOMER_RG_NAME}"
done

az identity create -n "${service_managed_identity_uami_name}" -g "${CUSTOMER_RG_NAME}"
