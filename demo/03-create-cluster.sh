#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

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
  echo "user-assigned identity ${uami_name} created"
}

assign_roles_to_managed_identities() {
  # Reference: test/e2e-setup/bicep/modules/managed-identities.bicep

  # Get VNet ID
  local vnet_id
  vnet_id=$(az network vnet show \
    --resource-group "${CUSTOMER_RG_NAME}" \
    --name "${CUSTOMER_VNET_NAME}" \
    --query id \
    --output tsv)

  # Role Definition IDs (from Azure built-in roles for ARO-HCP)
  # Source: test/e2e-setup/bicep/modules/managed-identities.bicep
  # TODO: Retrieve these dynamically from the HcpOperatorIdentityRoleSets API once available:

  #   GET /subscriptions/{subscriptionId}/providers/Microsoft.RedHatOpenShift/locations/{location}/hcpOperatorIdentityRoleSets/{version}?api-version=2024-06-10-preview
  local hcp_service_mi_role="c0ff367d-66d8-445e-917c-583feb0ef0d4"           # HCP Service Managed Identity
  local hcp_cluster_api_provider_role="88366f10-ed47-4cc0-9fab-c8a06148393e" # HCP Cluster API Provider
  local hcp_control_plane_operator_role="fc0c873f-45e9-4d0d-a7d1-585aab30c6ed" # HCP Control Plane Operator
  local cloud_controller_manager_role="a1f96423-95ce-4224-ab27-4e3dc72facd4"  # Cloud Controller Manager
  local ingress_operator_role="0336e1d3-7a87-462b-b6db-342b63f7802c"          # Cluster Ingress Operator
  local file_storage_operator_role="0d7aedc0-15fd-4a67-a412-efad370c947e"     # File Storage Operator
  local network_operator_role="be7a6435-15ae-4171-8f30-4a343eff9e8f"          # Network Operator
  local key_vault_crypto_user_role="12338af0-0e69-4776-bea7-57ae8d297424"     # Key Vault Crypto User
  local federated_credentials_role="ef318e2a-8334-4a05-9e4a-295a196c6a6e"     # Federated Credentials
  local reader_role="acdd72a7-3385-48ef-bd42-f606fba81ae7"                    # Reader

  # Helper function to get principal ID
  get_principal_id() {
    local uami_name=$1
    az identity show \
      --name "${uami_name}" \
      --resource-group "${CUSTOMER_RG_NAME}" \
      --query principalId \
      --output tsv
  }

  # Helper function to assign role
  assign_role() {
    local principal_id=$1
    local role_id=$2
    local scope=$3
    local description=$4
    echo "  Assigning ${description}..."
    az role assignment create \
      --assignee-object-id "${principal_id}" \
      --assignee-principal-type ServicePrincipal \
      --role "${role_id}" \
      --scope "${scope}" \
      --output none 2>/dev/null || echo "    (may already exist)"
  }

  echo ""
  echo "=========================================="
  echo "=== ASSIGNING ROLES TO MANAGED IDENTITIES ==="
  echo "=========================================="

  # ============================================
  # SERVICE MANAGED IDENTITY ROLES
  # ============================================
  echo ""
  echo "=== Service Managed Identity ==="
  local service_mi_principal_id
  service_mi_principal_id=$(get_principal_id "${service_managed_identity_uami_name}")

  assign_role "${service_mi_principal_id}" "${hcp_service_mi_role}" "${vnet_id}" "HCP Service MI role on VNet"
  assign_role "${service_mi_principal_id}" "${hcp_service_mi_role}" "${SUBNET_ID}" "HCP Service MI role on Subnet"
  assign_role "${service_mi_principal_id}" "${hcp_service_mi_role}" "${NSG_ID}" "HCP Service MI role on NSG"

  # Reader role on all control plane identities
  for uami_name in "${CONTROL_PLANE_IDENTITIES_UAMIS_NAMES[@]}"
  do
    local uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${uami_name}"
    assign_role "${service_mi_principal_id}" "${reader_role}" "${uami_resource_id}" "Reader on ${uami_name}"
  done

  # Federated credentials role on data plane identities
  for uami_name in "${DATA_PLANE_IDENTITIES_UAMIS_NAMES[@]}"
  do
    local uami_resource_id="${UAMIS_RESOURCE_IDS_PREFIX}/${uami_name}"
    assign_role "${service_mi_principal_id}" "${federated_credentials_role}" "${uami_resource_id}" "Federated Credentials on ${uami_name}"
  done

  # ============================================
  # CLUSTER-API-AZURE ROLES
  # ============================================
  echo ""
  echo "=== cluster-api-azure Identity ==="
  local cluster_api_azure_mi="${USER}-${CLUSTER_NAME}-cp-cluster-api-azure-${OPERATORS_UAMIS_SUFFIX}"
  local cluster_api_azure_principal_id
  cluster_api_azure_principal_id=$(get_principal_id "${cluster_api_azure_mi}")

  assign_role "${cluster_api_azure_principal_id}" "${hcp_cluster_api_provider_role}" "${SUBNET_ID}" "HCP Cluster API Provider on Subnet"

  # ============================================
  # CONTROL-PLANE ROLES
  # ============================================
  echo ""
  echo "=== control-plane Identity ==="
  local control_plane_mi="${USER}-${CLUSTER_NAME}-cp-control-plane-${OPERATORS_UAMIS_SUFFIX}"
  local control_plane_principal_id
  control_plane_principal_id=$(get_principal_id "${control_plane_mi}")

  assign_role "${control_plane_principal_id}" "${hcp_control_plane_operator_role}" "${vnet_id}" "HCP Control Plane Operator on VNet"
  assign_role "${control_plane_principal_id}" "${hcp_control_plane_operator_role}" "${NSG_ID}" "HCP Control Plane Operator on NSG"

  # ============================================
  # CLOUD-CONTROLLER-MANAGER ROLES
  # ============================================
  echo ""
  echo "=== cloud-controller-manager Identity ==="
  local ccm_mi="${USER}-${CLUSTER_NAME}-cp-cloud-controller-manager-${OPERATORS_UAMIS_SUFFIX}"
  local ccm_principal_id
  ccm_principal_id=$(get_principal_id "${ccm_mi}")

  assign_role "${ccm_principal_id}" "${cloud_controller_manager_role}" "${SUBNET_ID}" "Cloud Controller Manager on Subnet"
  assign_role "${ccm_principal_id}" "${cloud_controller_manager_role}" "${NSG_ID}" "Cloud Controller Manager on NSG"

  # ============================================
  # INGRESS ROLES
  # ============================================
  echo ""
  echo "=== ingress Identity ==="
  local ingress_mi="${USER}-${CLUSTER_NAME}-cp-ingress-${OPERATORS_UAMIS_SUFFIX}"
  local ingress_principal_id
  ingress_principal_id=$(get_principal_id "${ingress_mi}")

  assign_role "${ingress_principal_id}" "${ingress_operator_role}" "${SUBNET_ID}" "Ingress Operator on Subnet"

  # ============================================
  # FILE-CSI-DRIVER (Control Plane) ROLES
  # ============================================
  echo ""
  echo "=== file-csi-driver (CP) Identity ==="
  local file_csi_cp_mi="${USER}-${CLUSTER_NAME}-cp-file-csi-driver-${OPERATORS_UAMIS_SUFFIX}"
  local file_csi_cp_principal_id
  file_csi_cp_principal_id=$(get_principal_id "${file_csi_cp_mi}")

  assign_role "${file_csi_cp_principal_id}" "${file_storage_operator_role}" "${SUBNET_ID}" "File Storage Operator on Subnet"
  assign_role "${file_csi_cp_principal_id}" "${file_storage_operator_role}" "${NSG_ID}" "File Storage Operator on NSG"

  # ============================================
  # CLOUD-NETWORK-CONFIG ROLES
  # ============================================
  echo ""
  echo "=== cloud-network-config Identity ==="
  local cloud_network_mi="${USER}-${CLUSTER_NAME}-cp-cloud-network-config-${OPERATORS_UAMIS_SUFFIX}"
  local cloud_network_principal_id
  cloud_network_principal_id=$(get_principal_id "${cloud_network_mi}")

  assign_role "${cloud_network_principal_id}" "${network_operator_role}" "${SUBNET_ID}" "Network Operator on Subnet"
  assign_role "${cloud_network_principal_id}" "${network_operator_role}" "${vnet_id}" "Network Operator on VNet"

  # ============================================
  # KMS ROLES
  # ============================================
  echo ""
  echo "=== kms Identity ==="
  local kms_mi="${USER}-${CLUSTER_NAME}-cp-kms-${OPERATORS_UAMIS_SUFFIX}"
  local kms_principal_id
  kms_principal_id=$(get_principal_id "${kms_mi}")

  # Get Key Vault ID
  local keyvault_id
  keyvault_id=$(az keyvault show \
    --name "${CUSTOMER_KV_NAME}" \
    --resource-group "${CUSTOMER_RG_NAME}" \
    --query id \
    --output tsv)

  assign_role "${kms_principal_id}" "${key_vault_crypto_user_role}" "${keyvault_id}" "Key Vault Crypto User on Key Vault"

  # ============================================
  # FILE-CSI-DRIVER (Data Plane) ROLES
  # ============================================
  echo ""
  echo "=== file-csi-driver (DP) Identity ==="
  local file_csi_dp_mi="${USER}-${CLUSTER_NAME}-dp-file-csi-driver-${OPERATORS_UAMIS_SUFFIX}"
  local file_csi_dp_principal_id
  file_csi_dp_principal_id=$(get_principal_id "${file_csi_dp_mi}")

  assign_role "${file_csi_dp_principal_id}" "${file_storage_operator_role}" "${SUBNET_ID}" "File Storage Operator on Subnet"
  assign_role "${file_csi_dp_principal_id}" "${file_storage_operator_role}" "${NSG_ID}" "File Storage Operator on NSG"

  echo ""
  echo "=========================================="
  echo "=== ALL ROLE ASSIGNMENTS COMPLETE ==="
  echo "=========================================="
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

  # Assign required roles to all managed identities
  # Reference: test/e2e-setup/bicep/modules/managed-identities.bicep
  assign_roles_to_managed_identities

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
