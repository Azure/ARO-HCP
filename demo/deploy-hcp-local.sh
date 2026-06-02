#!/bin/bash
# Create an ARO HCP Cluster via the personal dev RP frontend (port-forward).
#
# This script:
# 1. Creates customer infrastructure (VNet, NSG, KeyVault) via ARM bicep
# 2. Creates managed identities and role assignments via ARM bicep
# 3. Registers the subscription with the personal dev RP
# 4. PUTs the HCP cluster directly to the port-forwarded frontend
# 5. Waits for cluster to reach Succeeded state
# 6. PUTs a node pool
#
# The script is idempotent — safe to re-run. It skips steps that are
# already complete (cluster already Succeeded, nodepool already exists).
#
# Port-forward is managed automatically. If port 8443 is already in use,
# the existing port-forward is reused.
#
# Usage:
#   ./demo/deploy-hcp-local.sh
#   CLUSTER_NAME=my-test CUSTOMER_RG_NAME=my-rg ./demo/deploy-hcp-local.sh

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

source "${SCRIPT_DIR}/env_vars"
LOCATION=${LOCATION:-westus3}
SUBSCRIPTION=$(az account show --query 'id' -o tsv)
TENANT_ID=$(az account show --query 'tenantId' -o tsv)
FRONTEND_PORT=${FRONTEND_PORT:-8443}
FRONTEND_URL=${FRONTEND_URL:-http://localhost:${FRONTEND_PORT}}
CLUSTER_VERSION=${CLUSTER_VERSION:-"4.20"}
NODEPOOL_VERSION=${NODEPOOL_VERSION:-"4.20.16"}
PRIVATE_KEYVAULT=${PRIVATE_KEYVAULT:-true}
MANAGED_RESOURCE_GROUP="${CLUSTER_NAME}-rg-03"

# Port-forward management
PF_PID=""
cleanup() {
    if [[ -n "${PF_PID}" ]]; then
        kill "${PF_PID}" 2>/dev/null || true
    fi
}
trap cleanup EXIT

setup_port_forward() {
    if lsof -i ":${FRONTEND_PORT}" &>/dev/null; then
        echo "    Port ${FRONTEND_PORT} already in use, reusing existing port-forward"
        return
    fi

    echo "    Setting up port-forward to frontend..."
    SVC_KUBECONFIG="${SVC_KUBECONFIG:-$(make -C "${REPO_ROOT}" -s infra.svc.aks.kubeconfigfile 2>/dev/null || echo "")}"
    if [[ -z "${SVC_KUBECONFIG}" ]]; then
        echo "    ERROR: Cannot determine service cluster kubeconfig."
        echo "    Set SVC_KUBECONFIG env var or run 'make infra.svc.aks.kubeconfig' first."
        exit 1
    fi
    kubectl --kubeconfig="${SVC_KUBECONFIG}" port-forward svc/aro-hcp-frontend "${FRONTEND_PORT}:8443" -n aro-hcp &
    PF_PID=$!
    sleep 5

    if ! curl --silent --max-time 5 -o /dev/null -w "%{http_code}" "${FRONTEND_URL}/subscriptions" 2>/dev/null | grep -q '^[0-9]'; then
        echo "    ERROR: Port-forward started but frontend is not responding."
        exit 1
    fi
    echo "    Port-forward established (PID ${PF_PID})"
}

check_frontend() {
    if ! curl --silent --max-time 5 -o /dev/null "${FRONTEND_URL}/subscriptions" 2>/dev/null; then
        echo "    ERROR: Lost connection to frontend. Port-forward may have died."
        exit 1
    fi
}

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
  --template-file "${SCRIPT_DIR}/bicep/customer-infra.bicep" \
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
  --template-file "${SCRIPT_DIR}/bicep/cluster-prereqs.bicep" \
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

# Set up port-forward before any frontend calls
setup_port_forward

# Step 4: Register subscription with personal dev RP
echo "==> Step 4: Registering subscription with dev RP"
check_frontend
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

check_frontend
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
echo "==> Step 6: Waiting for cluster to reach Succeeded state..."

# Check current state first — skip waiting if already Succeeded
CLUSTER_STATE=$(curl --silent --max-time 10 \
  --header "X-Ms-Client-Principal-Name: deploy-script" \
  "${FRONTEND_URL}${CLUSTER_RESOURCE_ID}?api-version=${API_VERSION}" \
  | python3 -c "import json,sys; print(json.load(sys.stdin).get('properties',{}).get('provisioningState','unknown'))" 2>/dev/null || echo "unknown")

if [[ "${CLUSTER_STATE}" == "Succeeded" ]]; then
  echo "    Cluster already Succeeded, skipping wait"
else
  MAX_WAIT=1800
  WAITED=0
  while true; do
    check_frontend
    CLUSTER_STATE=$(curl --silent --max-time 10 \
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
fi

# Step 7: Create node pool
echo "==> Step 7: Creating node pool ${NP_NAME}"

# Check if nodepool already exists
check_frontend
NP_STATE=$(curl --silent --max-time 10 \
  --header "X-Ms-Client-Principal-Name: deploy-script" \
  "${FRONTEND_URL}${NODE_POOL_RESOURCE_ID}?api-version=${API_VERSION}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('properties',{}).get('provisioningState','notfound'))" 2>/dev/null || echo "notfound")

if [[ "${NP_STATE}" == "Succeeded" ]]; then
  echo "    Node pool already exists (state: Succeeded), skipping"
else
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

  check_frontend
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
fi

echo ""
echo "==> Done"
echo "    Cluster:    ${CLUSTER_RESOURCE_ID}"
echo "    Node Pool:  ${NODE_POOL_RESOURCE_ID}"
echo ""
echo "    Monitor cluster status:"
echo "    curl -s ${FRONTEND_URL}${CLUSTER_RESOURCE_ID}?api-version=${API_VERSION} | jq .properties.provisioningState"
