#!/bin/bash
# Create an ARO HCP Cluster via the personal dev RP frontend (port-forward).
#
# This script:
# 1. Creates customer infrastructure (VNet, NSG, KeyVault) via ARM bicep
# 2. Creates managed identities and role assignments via ARM bicep
# 3. Registers the subscription with the personal dev RP
# 4. PUTs the HCP cluster directly to the port-forwarded frontend
# 5. PUTs a node pool
#
# Prerequisites:
#   - Port-forward to the frontend: kubectl port-forward svc/aro-hcp-frontend 8443:8443 -n aro-hcp
#   - az CLI logged in
#
# Usage:
#   kubectl port-forward svc/aro-hcp-frontend 8443:8443 -n aro-hcp &
#   ./demo/deploy-hcp-local.sh

set -o errexit
set -o nounset
set -o pipefail

source env_vars
LOCATION=${LOCATION:-westus3}
SUBSCRIPTION=$(az account show --query 'id' -o tsv)
TENANT_ID=$(az account show --query 'tenantId' -o tsv)
FRONTEND_URL=${FRONTEND_URL:-http://localhost:8443}
CLUSTER_VERSION=${CLUSTER_VERSION:-"4.20"}
NODEPOOL_VERSION=${NODEPOOL_VERSION:-"4.20.16"}
PRIVATE_KEYVAULT=${PRIVATE_KEYVAULT:-true}
MANAGED_RESOURCE_GROUP="${CLUSTER_NAME}-rg-03"

echo "==> Creating HCP cluster via personal dev RP"
echo "    Frontend:  ${FRONTEND_URL}"
echo "    Location:  ${LOCATION}"
echo "    Cluster:   ${CLUSTER_NAME}"
echo ""

# Step 1: Create resource group
echo "==> Step 1: Creating resource group ${CUSTOMER_RG_NAME}"
az group create \
  --name "${CUSTOMER_RG_NAME}" \
  --subscription "${SUBSCRIPTION}" \
  --location "${LOCATION}" \
  -o none

# Step 2: Deploy customer infrastructure (VNet, NSG, KeyVault)
echo "==> Step 2: Deploying customer infrastructure"
az deployment group create \
  --name 'infra' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file bicep/customer-infra.bicep \
  --parameters \
    customerNsgName="${CUSTOMER_NSG}" \
    customerVnetName="${CUSTOMER_VNET_NAME}" \
    customerVnetSubnetName="${CUSTOMER_VNET_SUBNET1}" \
    customerVirtualNetworkIntegrationSubnetName="${CUSTOMER_VNET_INTEGRATION_SUBNET}" \
    privateKeyVault=${PRIVATE_KEYVAULT} \
  -o none

KEYVAULT_NAME=$(az deployment group show \
  --name 'infra' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --query "properties.outputs.keyVaultName.value" -o tsv)

# Step 3: Deploy managed identities and role assignments (without HCP resource)
echo "==> Step 3: Deploying managed identities and role assignments"
az deployment group create \
  --name 'cluster-prereqs' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file bicep/cluster-prereqs.bicep \
  --parameters \
    vnetName="${CUSTOMER_VNET_NAME}" \
    subnetName="${CUSTOMER_VNET_SUBNET1}" \
    vnetIntegrationSubnetName="${CUSTOMER_VNET_INTEGRATION_SUBNET}" \
    nsgName="${CUSTOMER_NSG}" \
    clusterName="${CLUSTER_NAME}" \
    managedResourceGroupName="${MANAGED_RESOURCE_GROUP}" \
    keyVaultName="${KEYVAULT_NAME}" \
    privateKeyVault=${PRIVATE_KEYVAULT} \
    clusterVersion="${CLUSTER_VERSION}" \
  -o none

# Collect MI resource IDs from deployment outputs
echo "==> Collecting managed identity IDs"
get_output() {
  az deployment group show \
    --name 'cluster-prereqs' \
    --subscription "${SUBSCRIPTION}" \
    --resource-group "${CUSTOMER_RG_NAME}" \
    --query "properties.outputs.${1}.value" -o tsv
}

CLUSTER_API_AZURE_MI=$(get_output clusterApiAzureMiId)
CONTROL_PLANE_MI=$(get_output controlPlaneMiId)
CLOUD_CONTROLLER_MI=$(get_output cloudControllerManagerMiId)
INGRESS_MI=$(get_output ingressMiId)
DISK_CSI_MI=$(get_output diskCsiDriverMiId)
FILE_CSI_MI=$(get_output fileCsiDriverMiId)
IMAGE_REGISTRY_MI=$(get_output imageRegistryMiId)
CLOUD_NETWORK_MI=$(get_output cloudNetworkConfigMiId)
KMS_MI=$(get_output kmsMiId)
SERVICE_MI=$(get_output serviceManagedIdentityId)
DP_DISK_CSI_MI=$(get_output dpDiskCsiDriverMiId)
DP_FILE_CSI_MI=$(get_output dpFileCsiDriverMiId)
DP_IMAGE_REGISTRY_MI=$(get_output dpImageRegistryMiId)
SUBNET_ID=$(get_output subnetId)
VNET_INTEGRATION_SUBNET_ID=$(get_output vnetIntegrationSubnetId)
NSG_ID=$(get_output nsgId)
ETCD_KEY_VERSION=$(get_output etcdEncryptionKeyVersion)

# Step 4: Register subscription with personal dev RP
echo "==> Step 4: Registering subscription with dev RP"
curl --silent --show-error \
  --request PUT \
  --header "Content-Type: application/json" \
  --data '{"state":"Registered","registrationDate":"now","properties":{"tenantId":"'"${TENANT_ID}"'","registeredFeatures":[{"name":"Microsoft.RedHatOpenShift/ExperimentalReleaseFeatures","state":"Registered"}]}}' \
  "${FRONTEND_URL}/subscriptions/${SUBSCRIPTION}?api-version=2.0" \
  -o /dev/null

echo "    Subscription registered"

# Step 5: PUT HCP cluster to frontend
echo "==> Step 5: Creating HCP cluster ${CLUSTER_NAME}"

ETCD_CONFIG='"dataEncryption":{"keyManagementMode":"CustomerManaged","customerManaged":{"encryptionType":"KMS","kms":{"activeKey":{"name":"etcd-data-kms-encryption-key","version":"'"${ETCD_KEY_VERSION}"'"},"vaultName":"'"${KEYVAULT_NAME}"'","visibility":"'"$([ "${PRIVATE_KEYVAULT}" = "true" ] && echo "Private" || echo "Public")"'"}}}'

SYSTEM_DATA='{"createdBy":"deploy-hcp-local","createdByType":"Application","createdAt":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"}'
API_VERSION="2025-12-23-preview"

CLUSTER_PAYLOAD=$(cat <<EOF
{
  "location": "${LOCATION}",
  "properties": {
    "dns": {},
    "network": {
      "networkType": "OVNKubernetes",
      "podCidr": "10.128.0.0/14",
      "serviceCidr": "172.30.0.0/16",
      "machineCidr": "10.0.0.0/16",
      "hostPrefix": 23
    },
    "console": {},
    "etcd": {${ETCD_CONFIG}},
    "api": {"visibility": "Public"},
    "clusterImageRegistry": {"state": "Enabled"},
    "platform": {
      "managedResourceGroup": "${MANAGED_RESOURCE_GROUP}",
      "subnetId": "${SUBNET_ID}",
      "vnetIntegrationSubnetId": "${VNET_INTEGRATION_SUBNET_ID}",
      "outboundType": "LoadBalancer",
      "networkSecurityGroupId": "${NSG_ID}",
      "operatorsAuthentication": {
        "userAssignedIdentities": {
          "controlPlaneOperators": {
            "cluster-api-azure": "${CLUSTER_API_AZURE_MI}",
            "control-plane": "${CONTROL_PLANE_MI}",
            "cloud-controller-manager": "${CLOUD_CONTROLLER_MI}",
            "ingress": "${INGRESS_MI}",
            "disk-csi-driver": "${DISK_CSI_MI}",
            "file-csi-driver": "${FILE_CSI_MI}",
            "image-registry": "${IMAGE_REGISTRY_MI}",
            "cloud-network-config": "${CLOUD_NETWORK_MI}",
            "kms": "${KMS_MI}"
          },
          "dataPlaneOperators": {
            "disk-csi-driver": "${DP_DISK_CSI_MI}",
            "file-csi-driver": "${DP_FILE_CSI_MI}",
            "image-registry": "${DP_IMAGE_REGISTRY_MI}"
          },
          "serviceManagedIdentity": "${SERVICE_MI}"
        }
      }
    },
    "version": {"id": "${CLUSTER_VERSION}"}
  },
  "identity": {
    "type": "UserAssigned",
    "userAssignedIdentities": {
      "${SERVICE_MI}": {},
      "${CLUSTER_API_AZURE_MI}": {},
      "${CONTROL_PLANE_MI}": {},
      "${CLOUD_CONTROLLER_MI}": {},
      "${INGRESS_MI}": {},
      "${DISK_CSI_MI}": {},
      "${FILE_CSI_MI}": {},
      "${IMAGE_REGISTRY_MI}": {},
      "${CLOUD_NETWORK_MI}": {},
      "${KMS_MI}": {}
    }
  }
}
EOF
)

HTTP_CODE=$(curl --silent --show-error --output /tmp/hcp-create-response.json --write-out "%{http_code}" \
  --request PUT \
  --header "Content-Type: application/json" \
  --header "X-Ms-Arm-Resource-System-Data: ${SYSTEM_DATA}" \
  --header "X-Ms-Identity-Url: https://dummyhost.identity.azure.net" \
  --data "${CLUSTER_PAYLOAD}" \
  "${FRONTEND_URL}${CLUSTER_RESOURCE_ID}?api-version=${API_VERSION}")

if [ "${HTTP_CODE}" -ge 200 ] && [ "${HTTP_CODE}" -lt 300 ]; then
  echo "    Cluster creation accepted (HTTP ${HTTP_CODE})"
else
  echo "    ERROR: Cluster creation failed (HTTP ${HTTP_CODE})"
  cat /tmp/hcp-create-response.json
  exit 1
fi

# Step 6: Wait for cluster to be ready for node pool creation
echo "==> Step 6: Waiting for cluster to reach an updatable state..."
MAX_WAIT=1800
WAITED=0
while true; do
  CLUSTER_STATE=$(curl --silent --show-error \
    --header "X-Ms-Client-Principal-Name: deploy-script" \
    "${FRONTEND_URL}${CLUSTER_RESOURCE_ID}?api-version=${API_VERSION}" \
    | python3 -c "import json,sys; print(json.load(sys.stdin).get('properties',{}).get('provisioningState','unknown'))" 2>/dev/null || echo "unknown")

  if [[ "${CLUSTER_STATE}" == "Succeeded" || "${CLUSTER_STATE}" == "Failed" ]]; then
    echo "    Cluster state: ${CLUSTER_STATE}"
    break
  fi

  if [[ ${WAITED} -ge ${MAX_WAIT} ]]; then
    echo "    WARNING: Timed out waiting (${MAX_WAIT}s). Current state: ${CLUSTER_STATE}"
    echo "    Attempting node pool creation anyway..."
    break
  fi

  echo "    Cluster state: ${CLUSTER_STATE} (waited ${WAITED}s)..."
  sleep 30
  WAITED=$((WAITED + 30))
done

if [[ "${CLUSTER_STATE}" == "Failed" ]]; then
  echo "    ERROR: Cluster provisioning failed"
  exit 1
fi

# Step 7: Create node pool
echo "==> Step 7: Creating node pool ${NP_NAME}"

NP_PAYLOAD=$(cat <<EOF
{
  "location": "${LOCATION}",
  "properties": {
    "version": {"id": "${NODEPOOL_VERSION}"},
    "platform": {
      "vmSize": "Standard_D4as_v5",
      "subnetId": "${SUBNET_ID}"
    },
    "replicas": 2,
    "autoRepair": true
  }
}
EOF
)

HTTP_CODE=$(curl --silent --show-error --output /tmp/np-create-response.json --write-out "%{http_code}" \
  --request PUT \
  --header "Content-Type: application/json" \
  --header "X-Ms-Arm-Resource-System-Data: ${SYSTEM_DATA}" \
  --header "X-Ms-Identity-Url: https://dummyhost.identity.azure.net" \
  --data "${NP_PAYLOAD}" \
  "${FRONTEND_URL}${NODE_POOL_RESOURCE_ID}?api-version=${API_VERSION}")

if [ "${HTTP_CODE}" -ge 200 ] && [ "${HTTP_CODE}" -lt 300 ]; then
  echo "    Node pool creation accepted (HTTP ${HTTP_CODE})"
else
  echo "    ERROR: Node pool creation failed (HTTP ${HTTP_CODE})"
  cat /tmp/np-create-response.json
  exit 1
fi

echo ""
echo "==> Done"
echo "    Cluster:    ${CLUSTER_RESOURCE_ID}"
echo "    Node Pool:  ${NODE_POOL_RESOURCE_ID}"
echo ""
echo "    Monitor cluster status:"
echo "    curl -s ${FRONTEND_URL}${CLUSTER_RESOURCE_ID}?api-version=${API_VERSION} | jq .properties.provisioningState"
