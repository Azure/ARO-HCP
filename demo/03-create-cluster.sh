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
          "encryptionType": "kms",
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

  KEY_VAULT_KEY_VERSION=$(az keyvault key show --vault-name ${CUSTOMER_KV_NAME} --name "${ETCD_ENCRYPTION_KEY_NAME}" | jq .key.kid -r | awk -F'/' '{print $NF}')
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
