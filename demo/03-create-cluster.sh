#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

# Azure built-in role definition GUIDs — kept in sync with demo/bicep/cluster.bicep
# (same assignments as non-PERS environments get from Bicep).
readonly AZURE_ROLE_READER='acdd72a7-3385-48ef-bd42-f606fba81ae7'
readonly AZURE_ROLE_HCP_CLUSTER_API_PROVIDER='88366f10-ed47-4cc0-9fab-c8a06148393e'
readonly AZURE_ROLE_KEY_VAULT_CRYPTO_USER='12338af0-0e69-4776-bea7-57ae8d297424'
readonly AZURE_ROLE_HCP_CONTROL_PLANE_OPERATOR='fc0c873f-45e9-4d0d-a7d1-585aab30c6ed'
readonly AZURE_ROLE_CLOUD_CONTROLLER_MANAGER='a1f96423-95ce-4224-ab27-4e3dc72facd4'
readonly AZURE_ROLE_INGRESS_OPERATOR='0336e1d3-7a87-462b-b6db-342b63f7802c'
readonly AZURE_ROLE_FILE_STORAGE_OPERATOR='0d7aedc0-15fd-4a67-a412-efad370c947e'
readonly AZURE_ROLE_NETWORK_OPERATOR='be7a6435-15ae-4171-8f30-4a343eff9e8f'
readonly AZURE_ROLE_FEDERATED_CREDENTIALS='ef318e2a-8334-4a05-9e4a-295a196c6a6e'
readonly AZURE_ROLE_HCP_SERVICE_MANAGED_IDENTITY='c0ff367d-66d8-445e-917c-583feb0ef0d4'

role_definition_resource_id() {
  local role_guid="$1"
  echo "${SUBSCRIPTION_RESOURCE_ID}/providers/Microsoft.Authorization/roleDefinitions/${role_guid}"
}

identity_principal_id() {
  az identity show --ids "$1" --query principalId --output tsv
}

# Assign a role to a user-assigned managed identity (ServicePrincipal) at scope.
# Succeeds if the same assignment already exists (idempotent for re-runs).
assign_uami_role_at_scope() {
  local role_guid="$1"
  local assignee_principal_id="$2"
  local scope="$3"
  local role_id
  role_id=$(role_definition_resource_id "${role_guid}")
  local output
  if output=$(az role assignment create \
    --role "${role_id}" \
    --assignee-object-id "${assignee_principal_id}" \
    --assignee-principal-type ServicePrincipal \
    --scope "${scope}" \
    --only-show-errors 2>&1); then
    return 0
  fi
  if echo "${output}" | grep -qiE 'Conflict|already exists|RoleAssignmentExists'; then
    return 0
  fi
  echo "${output}" >&2
  return 1
}

control_plane_operator_identity_principal_id() {
  local operator_name="$1"
  local i
  for i in "${!CONTROL_PLANE_OPERATORS_NAMES[@]}"; do
    if [[ "${CONTROL_PLANE_OPERATORS_NAMES[$i]}" == "${operator_name}" ]]; then
      identity_principal_id "${UAMIS_RESOURCE_IDS_PREFIX}/${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
      return 0
    fi
  done
  echo "unknown control plane operator: ${operator_name}" >&2
  return 1
}

data_plane_operator_identity_principal_id() {
  local operator_name="$1"
  local i
  for i in "${!DATA_PLANE_OPERATORS_NAMES[@]}"; do
    if [[ "${DATA_PLANE_OPERATORS_NAMES[$i]}" == "${operator_name}" ]]; then
      identity_principal_id "${UAMIS_RESOURCE_IDS_PREFIX}/${DATA_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
      return 0
    fi
  done
  echo "unknown data plane operator: ${operator_name}" >&2
  return 1
}

data_plane_operator_uami_name() {
  local operator_name="$1"
  local i
  for i in "${!DATA_PLANE_OPERATORS_NAMES[@]}"; do
    if [[ "${DATA_PLANE_OPERATORS_NAMES[$i]}" == "${operator_name}" ]]; then
      echo "${DATA_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
      return 0
    fi
  done
  return 1
}

# Role assignments required for PERS (script-created UAMIs); Bicep does this in shared envs.
create_managed_identity_role_assignments_for_cluster() {
  local vnet_id="$1"
  local subnet_id="$2"
  local nsg_id="$3"
  local key_vault_resource_id="$4"
  local service_mi_principal_id
  service_mi_principal_id=$(identity_principal_id "${UAMIS_RESOURCE_IDS_PREFIX}/${service_managed_identity_uami_name}")

  echo "Creating Azure role assignments for cluster managed identities (PERS / script path)..."

  # Service managed identity: HCP Service MI role on customer vnet, subnet, and NSG
  assign_uami_role_at_scope "${AZURE_ROLE_HCP_SERVICE_MANAGED_IDENTITY}" "${service_mi_principal_id}" "${vnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_HCP_SERVICE_MANAGED_IDENTITY}" "${service_mi_principal_id}" "${subnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_HCP_SERVICE_MANAGED_IDENTITY}" "${service_mi_principal_id}" "${nsg_id}"

  # Reader on each control-plane operator UAMI for the service managed identity (matches Bicep)
  local i
  for i in "${!CONTROL_PLANE_OPERATORS_NAMES[@]}"; do
    assign_uami_role_at_scope "${AZURE_ROLE_READER}" "${service_mi_principal_id}" \
      "${UAMIS_RESOURCE_IDS_PREFIX}/${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[$i]}"
  done

  local smi_cluster_api_pid smi_kms_pid smi_cp_pid smi_ccm_pid smi_ingress_pid smi_file_pid smi_net_pid
  smi_cluster_api_pid=$(control_plane_operator_identity_principal_id cluster-api-azure)
  smi_kms_pid=$(control_plane_operator_identity_principal_id kms)
  smi_cp_pid=$(control_plane_operator_identity_principal_id control-plane)
  smi_ccm_pid=$(control_plane_operator_identity_principal_id cloud-controller-manager)
  smi_ingress_pid=$(control_plane_operator_identity_principal_id ingress)
  smi_file_pid=$(control_plane_operator_identity_principal_id file-csi-driver)
  smi_net_pid=$(control_plane_operator_identity_principal_id cloud-network-config)

  assign_uami_role_at_scope "${AZURE_ROLE_HCP_CLUSTER_API_PROVIDER}" "${smi_cluster_api_pid}" "${subnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_KEY_VAULT_CRYPTO_USER}" "${smi_kms_pid}" "${key_vault_resource_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_HCP_CONTROL_PLANE_OPERATOR}" "${smi_cp_pid}" "${vnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_HCP_CONTROL_PLANE_OPERATOR}" "${smi_cp_pid}" "${nsg_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_CLOUD_CONTROLLER_MANAGER}" "${smi_ccm_pid}" "${subnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_CLOUD_CONTROLLER_MANAGER}" "${smi_ccm_pid}" "${nsg_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_INGRESS_OPERATOR}" "${smi_ingress_pid}" "${subnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_FILE_STORAGE_OPERATOR}" "${smi_file_pid}" "${subnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_FILE_STORAGE_OPERATOR}" "${smi_file_pid}" "${nsg_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_NETWORK_OPERATOR}" "${smi_net_pid}" "${subnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_NETWORK_OPERATOR}" "${smi_net_pid}" "${vnet_id}"

  # Data plane: federated-credentials role for service MI on each DP UAMI; file operator on subnet/NSG
  local dp_file_pid
  dp_file_pid=$(data_plane_operator_identity_principal_id file-csi-driver)

  assign_uami_role_at_scope "${AZURE_ROLE_FEDERATED_CREDENTIALS}" "${service_mi_principal_id}" "${UAMIS_RESOURCE_IDS_PREFIX}/$(data_plane_operator_uami_name disk-csi-driver)"
  assign_uami_role_at_scope "${AZURE_ROLE_FEDERATED_CREDENTIALS}" "${service_mi_principal_id}" "${UAMIS_RESOURCE_IDS_PREFIX}/$(data_plane_operator_uami_name file-csi-driver)"
  assign_uami_role_at_scope "${AZURE_ROLE_FEDERATED_CREDENTIALS}" "${service_mi_principal_id}" "${UAMIS_RESOURCE_IDS_PREFIX}/$(data_plane_operator_uami_name image-registry)"
  assign_uami_role_at_scope "${AZURE_ROLE_FILE_STORAGE_OPERATOR}" "${dp_file_pid}" "${subnet_id}"
  assign_uami_role_at_scope "${AZURE_ROLE_FILE_STORAGE_OPERATOR}" "${dp_file_pid}" "${nsg_id}"

  echo "Finished Azure role assignments for cluster managed identities."
}

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

initialize_etcd_encryption_json_map() {
  ETCD_ENCRYPTION_JSON_MAP=$(cat << 'EOF'
    {
      "dataEncryption": {
        "keyManagementMode": "CustomerManaged",
        "customerManaged": {
          "encryptionType": "KMS",
          "kms": {
            "activeKey": {
              "vaultName": "",
              "name": "",
              "version": ""
            }
          }
        }
      }
    }
EOF
)

  KEY_VAULT_KEY_VERSION=$(az rest --method GET --uri /subscriptions/${SUBSCRIPTION_ID}/resourcegroups/${CUSTOMER_RG_NAME}/providers/Microsoft.KeyVault/vaults/${CUSTOMER_KV_NAME}/keys/${ETCD_ENCRYPTION_KEY_NAME}/versions?api-version=2024-12-01-preview --output json | jq -r '.value[0].name')
  ETCD_ENCRYPTION_JSON_MAP=$(echo -n "${ETCD_ENCRYPTION_JSON_MAP}" | jq \
    --arg vault_name "$CUSTOMER_KV_NAME" \
    --arg key_name "$ETCD_ENCRYPTION_KEY_NAME" \
    --arg key_version "$KEY_VAULT_KEY_VERSION" \
    '
      .dataEncryption.customerManaged.kms.activeKey.vaultName = $vault_name |
      .dataEncryption.customerManaged.kms.activeKey.name = $key_name |
      .dataEncryption.customerManaged.kms.activeKey.version = $key_version
    '
  )
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
  echo "user-assigned identity ${service_managed_identity_uami_name} created"
}

main() {
  NSG_ID=$(az network nsg list --resource-group ${CUSTOMER_RG_NAME} --query "[?name=='${CUSTOMER_NSG}'].id" --output tsv)
  SUBNET_ID=$(az network vnet subnet show --resource-group ${CUSTOMER_RG_NAME} --vnet-name ${CUSTOMER_VNET_NAME} --name ${CUSTOMER_VNET_SUBNET1} --query id --output tsv)
  VNET_ID=$(az network vnet show --resource-group "${CUSTOMER_RG_NAME}" --name "${CUSTOMER_VNET_NAME}" --query id --output tsv)
  KEY_VAULT_RESOURCE_ID=$(az keyvault show --name "${CUSTOMER_KV_NAME}" --resource-group "${CUSTOMER_RG_NAME}" --query id --output tsv)

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

  # control plane operator names required for OCP 4.19.
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

  # data plane operator names required for OCP 4.19.
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

  create_managed_identity_role_assignments_for_cluster "${VNET_ID}" "${SUBNET_ID}" "${NSG_ID}" "${KEY_VAULT_RESOURCE_ID}"

  ETCD_ENCRYPTION_JSON_MAP=""
  initialize_etcd_encryption_json_map

  jq \
    --arg location "$LOCATION" \
    --arg managed_rg "$MANAGED_RESOURCE_GROUP" \
    --arg subnet_id "$SUBNET_ID" \
    --arg nsg_id "$NSG_ID" \
    --argjson uamis_json_map "$UAMIS_JSON_MAP" \
    --argjson identity_uamis_json_map "$IDENTITY_UAMIS_JSON_MAP" \
    --argjson etcd_encryption_json_map "$ETCD_ENCRYPTION_JSON_MAP" \
    '
      .location = $location |
      .properties.platform.managedResourceGroup = $managed_rg |
      .properties.platform.subnetId = $subnet_id |
      .properties.platform.networkSecurityGroupId = $nsg_id |
      .properties.platform.operatorsAuthentication.userAssignedIdentities = $uamis_json_map |
      .identity.userAssignedIdentities = $identity_uamis_json_map |
      .properties.etcd = $etcd_encryption_json_map
    ' "${CLUSTER_TMPL_FILE}" > ${CLUSTER_FILE}

  rp_put_request "${CLUSTER_RESOURCE_ID}" "@${CLUSTER_FILE}"
}

# Call to the `main` function in the script
main "$@"
