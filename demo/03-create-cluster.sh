#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

CONFIG_FILE="cluster-service/azure-operators-managed-identities-config.yaml"

initialize_control_plane_identities_uamis_names() {
  for operator_name in "${CONTROL_PLANE_OPERATORS_NAMES[@]}"
  do
    CONTROL_PLANE_IDENTITIES_UAMIS_NAMES+=("${USER}-${CLUSTER_NAME}-cp-${operator_name}-${OPERATORS_UAMIS_SUFFIX}")
  done
}

initialize_data_plane_identities_uamis_names() {
  for operator_name in "${DATA_PLANE_OPERATORS_NAMES[@]}"
  do
    DATA_PLANE_IDENTITIES_UAMIS_NAMES+=("${USER}-${CLUSTER_NAME}-dp-${operator_name}-${OPERATORS_UAMIS_SUFFIX}")
  done
}

initialize_uamis_json_map() {

UAMIS_JSON_MAP=$(cat << 'EOF'
{
  "controlPlaneOperators": {},
  "dataPlaneOperators": {},
  "serviceManagedIdentity": ""
}
EOF
)

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
    --arg operator_name $curr_operator_name \
    --arg uami_resource_id $curr_uami_resource_id \
    '
      .controlPlaneOperators[$operator_name] = $uami_resource_id
    '
  )
done

for i in "${!DATA_PLANE_IDENTITIES_UAMIS_NAMES[@]}"; do
  curr_operator_name="${DATA_PLANE_OPERATORS_NAMES[$i]}"
  curr_uami_name="${DATA_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
  curr_uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${curr_uami_name}"
  UAMIS_JSON_MAP=$(echo -n ${UAMIS_JSON_MAP} | jq \
    --arg operator_name $curr_operator_name \
    --arg uami_resource_id $curr_uami_resource_id \
    '
      .dataPlaneOperators[$operator_name] = $uami_resource_id
    '
  )
done

}

initialize_uamis_identity_json_map() {

IDENTITY_UAMIS_JSON_MAP=$(cat << 'EOF'
{
}
EOF
)

for i in "${!CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[@]}"; do
  curr_operator_name="${CONTROL_PLANE_OPERATORS_NAMES[$i]}"
  curr_uami_name="${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
  curr_uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${curr_uami_name}"
  IDENTITY_UAMIS_JSON_MAP=$(echo -n "${IDENTITY_UAMIS_JSON_MAP}" | jq \
    --arg operator_name $curr_operator_name \
    --arg uami_resource_id $curr_uami_resource_id \
    '
      .[$uami_resource_id] = {}
    '
  )
done

service_managed_identity_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${service_managed_identity_uami_name}"
IDENTITY_UAMIS_JSON_MAP=$(echo -n "${IDENTITY_UAMIS_JSON_MAP}" | jq \
    --arg uami_resource_id $service_managed_identity_resource_id \
    '
      .[$uami_resource_id] = {}
    '
  )

}

get_identity_principal_id() {
  local identity_name="$1"
  local resource_group="$2"
  
  az identity show \
    --name "${identity_name}" \
    --resource-group "${resource_group}" \
    --query principalId \
    --output tsv
}

assign_role_to_identity() {
  local principal_id="$1"
  local role_name="$2"
  local scope="$3"
  
  echo "Assigning role '${role_name}' to principal ID '${principal_id}' with scope '${scope}'"
  
  az role assignment create \
    --assignee-object-id "${principal_id}" \
    --assignee-principal-type ServicePrincipal \
    --role "${role_name}" \
    --scope "${scope}"
    
  echo "Role assignment completed successfully"
}

create_role_assignments_for_control_plane_identities() {
  if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "Warning: CONFIG_FILE not found at ${CONFIG_FILE}. Skipping control plane role assignments."
    return 0
  fi

  while read -r operator_name role_definition_resource_id; do
    if [[ -n "$operator_name" && -n "$role_definition_resource_id" ]]; then
      # Find the corresponding UAMI name
      for i in "${!CONTROL_PLANE_OPERATORS_NAMES[@]}"; do
        if [[ "${CONTROL_PLANE_OPERATORS_NAMES[$i]}" == "$operator_name" ]]; then
          uami_name="${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
          role_definition_id="/subscriptions/${SUBSCRIPTION_ID}${role_definition_resource_id}"

          # Get the principal ID of the managed identity
          principal_id=$(get_identity_principal_id "${uami_name}" "${CUSTOMER_RG_NAME}")
          
          if [[ -n "$principal_id" ]]; then
            # Assign role at resource group scope
            assign_role_to_identity "${principal_id}" "${role_definition_id}" "${RESOURCE_GROUP_RESOURCE_ID}"
          else
            echo "Error: Could not retrieve principal ID for identity ${uami_name}"
          fi
          break
        fi
      done
    fi
  done < <(yq '.controlPlaneOperatorsIdentities | to_entries[] | .key + " " + .value.roleDefinitions[0].resourceId' "$CONFIG_FILE")
}

create_role_assignments_for_data_plane_identities() {
  if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "Warning: CONFIG_FILE not found at ${CONFIG_FILE}. Skipping data plane role assignments."
    return 0
  fi

  while read -r operator_name role_definition_resource_id; do
    if [[ -n "$operator_name" && -n "$role_definition_resource_id" ]]; then
      # Find the corresponding UAMI name
      for i in "${!DATA_PLANE_OPERATORS_NAMES[@]}"; do
        if [[ "${DATA_PLANE_OPERATORS_NAMES[$i]}" == "$operator_name" ]]; then
          uami_name="${DATA_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
          role_definition_id="/subscriptions/${SUBSCRIPTION_ID}${role_definition_resource_id}"

          # Get the principal ID of the managed identity
          principal_id=$(get_identity_principal_id "${uami_name}" "${CUSTOMER_RG_NAME}")
          
          if [[ -n "$principal_id" ]]; then
            # Assign role at resource group scope
            assign_role_to_identity "${principal_id}" "${role_definition_id}" "${RESOURCE_GROUP_RESOURCE_ID}"
          else
            echo "Error: Could not retrieve principal ID for identity ${uami_name}"
          fi
          break
        fi
      done
    fi
  done < <(yq '.dataPlaneOperatorsIdentities | to_entries[] | .key + " " + .value.roleDefinitions[0].resourceId' "$CONFIG_FILE")
}

create_azure_managed_identities_for_cluster() {
  for uami_name in "${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[@]}"
  do
    echo "creating azure user-assigned identity ${uami_name} in resource group ${CUSTOMER_RG_NAME}"
    az identity create --name "${uami_name}" --resource-group "${CUSTOMER_RG_NAME}"
    echo "user-assigned identity ${uami_name} created"
  done

  for uami_name in "${DATA_PLANE_IDENTITIES_UAMIS_NAMES[@]}"
  do
    echo "creating azure user-assigned identity ${uami_name} in resource group ${CUSTOMER_RG_NAME}"
    az identity create --name "${uami_name}" --resource-group "${CUSTOMER_RG_NAME}"
    echo "user-assigned identity ${uami_name} created"
  done

  echo "creating azure user-assigned identity ${service_managed_identity_uami_name} in resource group ${CUSTOMER_RG_NAME}"
  az identity create --name "${service_managed_identity_uami_name}" --resource-group "${CUSTOMER_RG_NAME}"
  echo "user-assigned identity ${uami_name} created"
}

create_role_assignments_for_cluster() {
  echo "Creating role assignments for cluster identities..."
  
  #TODO : do we Wait for identities to be fully provisioned ?
  
  create_role_assignments_for_control_plane_identities
  create_role_assignments_for_data_plane_identities
  echo "All role assignments completed"
}

main() {
  NSG_ID=$(az network nsg list --resource-group ${CUSTOMER_RG_NAME} --query "[?name=='${CUSTOMER_NSG}'].id" --output tsv)
  SUBNET_ID=$(az network vnet subnet show --resource-group ${CUSTOMER_RG_NAME} --vnet-name ${CUSTOMER_VNET_NAME} --name ${CUSTOMER_VNET_SUBNET1} --query id --output tsv)

  UAMIS_RESOURCE_IDS_PREFIX="${RESOURCE_GROUP_RESOURCE_ID}/providers/Microsoft.ManagedIdentity/userAssignedIdentities"

  # A suffix that will be appended to all the
  # user-assigned managed identities names that
  # will be created for the cluster.
  # By default we generate a 6 characters random
  # suffix for the azure user-assigned managed
  # identites to be used by the cluster to be created.
  # The default can be overwritten by providing the
  # environment variable OPERATORS_UAMIS_SUFFIX
  # when running the script.
  : OPERATORS_UAMIS_SUFFIX=${OPERATORS_UAMIS_SUFFIX:=$(openssl rand -hex 3)}

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

  # We declare and initialize the control plane
  # user assigned identities names to the
  # empty array.
  CONTROL_PLANE_IDENTITIES_UAMIS_NAMES=()
  initialize_control_plane_identities_uamis_names

  # We declare and initialize the data plane
  # user assigned identities names to the
  # empty array.
  DATA_PLANE_IDENTITIES_UAMIS_NAMES=()
  initialize_data_plane_identities_uamis_names

  # We define the service managed identity
  # user assigned identity.
  service_managed_identity_uami_name="${USER}-${CLUSTER_NAME}-service-managed-identity-${OPERATORS_UAMIS_SUFFIX}"

  UAMIS_JSON_MAP=""
  initialize_uamis_json_map
  IDENTITY_UAMIS_JSON_MAP=""
  initialize_uamis_identity_json_map

  CURRENT_DATE=$(date -u +"%Y-%m-%dT%H:%M:%S+00:00")

  CLUSTER_TMPL_FILE="cluster.tmpl.json"
  CLUSTER_FILE="cluster.json"

  create_azure_managed_identities_for_cluster
  create_role_assignments_for_cluster

  jq \
    --arg location "$LOCATION" \
    --arg managed_rg "$MANAGED_RESOURCE_GROUP" \
    --arg subnet_id "$SUBNET_ID" \
    --arg nsg_id "$NSG_ID" \
    --argjson uamis_json_map "$UAMIS_JSON_MAP" \
    --argjson identity_uamis_json_map "$IDENTITY_UAMIS_JSON_MAP" \
    '
      .location = $location |
      .properties.platform.managedResourceGroup = $managed_rg |
      .properties.platform.subnetId = $subnet_id |
      .properties.platform.networkSecurityGroupId = $nsg_id |
      .properties.platform.operatorsAuthentication.userAssignedIdentities = $uamis_json_map |
      .identity.userAssignedIdentities = $identity_uamis_json_map
    ' "${CLUSTER_TMPL_FILE}" > ${CLUSTER_FILE}

  rp_put_request "${CLUSTER_RESOURCE_ID}" "@${CLUSTER_FILE}"
}

# Call to the `main` function in the script
main "$@"
