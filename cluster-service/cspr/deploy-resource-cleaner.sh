#!/bin/bash
set -euo pipefail

###############################################################################
# Deploy Resource Cleaner CronJob
#
# This script deploys the resource-cleaner CronJob to Kubernetes/OpenShift.
# It creates a ConfigMap from the actual script files, so they can be
# maintained and debugged separately.
#
# Usage:
#   ./deploy-resource-cleaner.sh [OPTIONS]
#
# Options:
#   --resource-group <rg>      Azure Resource Group where the managed identity will be created (required)
#   --cx-keyvault <name>       CX Key Vault name (required)
#   --mi-keyvault <name>       MI Key Vault name (required)
#   --acr-name <name>          Azure Container Registry name (required)
#   --maestro-url <url>        Maestro API URL (default: http://maestro.maestro.svc.cluster.local:8000)
#   --retention-hours <hours>  Retention period in hours (default: 3)
#   --help                     Show this help message
###############################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESOURCE_CLEANER_DIR="${SCRIPT_DIR}/resource-cleaner"

# Default values
RESOURCE_GROUP=""
CX_KEYVAULT=""
MI_KEYVAULT=""
ACR_NAME=""
MAESTRO_URL="http://maestro.maestro.svc.cluster.local:8000"
RETENTION_HOURS="3"
NAMESPACE="resource-cleaner"
MI_NAME="cspr-cleaner-mi"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --resource-group)
            RESOURCE_GROUP="$2"
            shift 2
            ;;
        --cx-keyvault)
            CX_KEYVAULT="$2"
            shift 2
            ;;
        --mi-keyvault)
            MI_KEYVAULT="$2"
            shift 2
            ;;
        --acr-name)
            ACR_NAME="$2"
            shift 2
            ;;
        --maestro-url)
            MAESTRO_URL="$2"
            shift 2
            ;;
        --retention-hours)
            RETENTION_HOURS="$2"
            shift 2
            ;;
        --help)
            head -n 22 "$0" | grep "^#" | sed 's/^# //' | sed 's/^#//'
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Validate required parameters
if [[ -z "${RESOURCE_GROUP}" ]]; then
    echo "ERROR: --resource-group is required"
    echo "Use --help for usage information"
    exit 1
fi

if [[ -z "${CX_KEYVAULT}" ]]; then
    echo "ERROR: --cx-keyvault is required"
    echo "Use --help for usage information"
    exit 1
fi

if [[ -z "${MI_KEYVAULT}" ]]; then
    echo "ERROR: --mi-keyvault is required"
    echo "Use --help for usage information"
    exit 1
fi

if [[ -z "${ACR_NAME}" ]]; then
    echo "ERROR: --acr-name is required"
    echo "Use --help for usage information"
    exit 1
fi

# Create or get managed identity
echo "========================================"
echo "Setting up managed identity: ${MI_NAME}"
echo "========================================"

# Create managed identity if it doesn't exist
if ! az identity show -g "${RESOURCE_GROUP}" -n "${MI_NAME}" &>/dev/null; then
    echo "Creating managed identity ${MI_NAME} in resource group ${RESOURCE_GROUP}..."
    az identity create -g "${RESOURCE_GROUP}" -n "${MI_NAME}"
    echo "✓ Managed identity created"
else
    echo "✓ Managed identity ${MI_NAME} already exists"
fi

# Get managed identity details
AZURE_CLIENT_ID=$(az identity show -g "${RESOURCE_GROUP}" -n "${MI_NAME}" --query clientId -o tsv)
AZURE_TENANT_ID=$(az account show --query tenantId -o tsv)
MI_PRINCIPAL_ID=$(az identity show -g "${RESOURCE_GROUP}" -n "${MI_NAME}" --query principalId -o tsv)

echo "  Client ID: ${AZURE_CLIENT_ID}"
echo "  Tenant ID: ${AZURE_TENANT_ID}"
echo "  Principal ID: ${MI_PRINCIPAL_ID}"
echo ""

# Get OIDC issuer URL from the cluster
echo "Fetching OIDC issuer URL from cluster..."
ISSUER_URL=$(kubectl get --raw /.well-known/openid-configuration | jq -r '.issuer')
if [[ -z "${ISSUER_URL}" ]]; then
    echo "ERROR: Failed to fetch OIDC issuer URL from cluster"
    echo "Make sure kubectl is configured and the cluster is accessible"
    exit 1
fi
echo "  OIDC Issuer URL: ${ISSUER_URL}"
echo ""

# Create federated credential for workload identity
echo "Creating federated credential for workload identity..."
FEDCRED_NAME="resource-cleaner-fedcred"
SUBJECT="system:serviceaccount:${NAMESPACE}:resource-cleaner-cronjob"

# Check if federated credential already exists
if az identity federated-credential show \
    --name "${FEDCRED_NAME}" \
    --identity-name "${MI_NAME}" \
    --resource-group "${RESOURCE_GROUP}" &>/dev/null; then
    echo "  ✓ Federated credential ${FEDCRED_NAME} already exists"
else
    echo "  Creating federated credential ${FEDCRED_NAME}..."
    az identity federated-credential create \
        --name "${FEDCRED_NAME}" \
        --identity-name "${MI_NAME}" \
        --resource-group "${RESOURCE_GROUP}" \
        --issuer "${ISSUER_URL}" \
        --subject "${SUBJECT}" \
        --audience "api://AzureADTokenExchange"
    echo "  ✓ Federated credential created"
fi
echo ""

# Assign permissions
echo "Assigning permissions to managed identity..."

# 1a. Key Vault Certificates Officer role on CX Key Vault (read/delete certificates)
echo "  Assigning Key Vault Certificates Officer role on CX Key Vault (${CX_KEYVAULT})..."
CX_KV_ID=$(az keyvault show -n "${CX_KEYVAULT}" --query id -o tsv)
az role assignment create \
    --role "Key Vault Certificates Officer" \
    --assignee-object-id "${MI_PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --scope "${CX_KV_ID}" \
    --output none 2>/dev/null || echo "    (role may already be assigned)"

# 1b. Key Vault Secrets Officer role on CX Key Vault (read/delete secrets)
echo "  Assigning Key Vault Secrets Officer role on CX Key Vault (${CX_KEYVAULT})..."
az role assignment create \
    --role "Key Vault Secrets Officer" \
    --assignee-object-id "${MI_PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --scope "${CX_KV_ID}" \
    --output none 2>/dev/null || echo "    (role may already be assigned)"

# 2. Key Vault Secrets Officer role on MI Key Vault (read/delete secrets)
echo "  Assigning Key Vault Secrets Officer role on MI Key Vault (${MI_KEYVAULT})..."
MI_KV_ID=$(az keyvault show -n "${MI_KEYVAULT}" --query id -o tsv)
az role assignment create \
    --role "Key Vault Secrets Officer" \
    --assignee-object-id "${MI_PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --scope "${MI_KV_ID}" \
    --output none 2>/dev/null || echo "    (role may already be assigned)"

# 3. AcrDelete role on ACR (read/delete tokens)
echo "  Assigning AcrDelete role on ACR (${ACR_NAME})..."
ACR_ID=$(az acr show -n "${ACR_NAME}" --query id -o tsv)
az role assignment create \
    --role "AcrDelete" \
    --assignee-object-id "${MI_PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --scope "${ACR_ID}" \
    --output none 2>/dev/null || echo "    (role may already be assigned)"

# 4. Contributor role on subscription (for resource group management)
# Note: The identity may already have this permission
echo "  Verifying Contributor permissions for resource group management..."
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
az role assignment create \
    --role "Contributor" \
    --assignee-object-id "${MI_PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --scope "/subscriptions/${SUBSCRIPTION_ID}" \
    --output none 2>/dev/null || echo "    (role may already be assigned)"

echo "✓ Permissions assigned"
echo ""

# Verify script directory exists
if [[ ! -d "${RESOURCE_CLEANER_DIR}" ]]; then
    echo "ERROR: Resource cleaner scripts directory not found: ${RESOURCE_CLEANER_DIR}"
    exit 1
fi

# Verify all required scripts exist
REQUIRED_SCRIPTS=(
    "common.sh"
    "resource-cleaner.sh"
    "retrieve-cluster-ids.sh"
    "cleanup-bundles.sh"
    "delete-resource-groups.sh"
    "delete-keyvault-secrets.sh"
    "delete-keyvault-certificates.sh"
    "cleanup-acr-tokens.sh"
)

echo "Verifying resource cleaner scripts..."
for script in "${REQUIRED_SCRIPTS[@]}"; do
    if [[ ! -f "${RESOURCE_CLEANER_DIR}/${script}" ]]; then
        echo "ERROR: Required script not found: ${script}"
        exit 1
    fi
    echo "  ✓ ${script}"
done

echo ""
echo "========================================"
echo "Deployment Configuration"
echo "========================================"
echo "  Resource Group: ${RESOURCE_GROUP}"
echo "  Managed Identity: ${MI_NAME}"
echo "  Azure Client ID: ${AZURE_CLIENT_ID}"
echo "  Azure Tenant ID: ${AZURE_TENANT_ID}"
echo "  OIDC Issuer URL: ${ISSUER_URL}"
echo "  CX Key Vault: ${CX_KEYVAULT}"
echo "  MI Key Vault: ${MI_KEYVAULT}"
echo "  ACR Name: ${ACR_NAME}"
echo "  Maestro URL: ${MAESTRO_URL}"
echo "  Retention Hours: ${RETENTION_HOURS}"
echo "  Namespace: ${NAMESPACE}"
echo ""

# Step 1: Create or update ConfigMap from script files
echo "Step 1: Creating ConfigMap from script files..."

# Create namespace if it doesn't exist
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# Step 1.5: Label namespace for Istio injection
echo "Step 1.5: Labeling namespace for Istio sidecar injection..."

# Get Istio version from aks-istio-system
ISTIO_VERSION=$(kubectl get deploy -n aks-istio-system -o name 2>/dev/null | \
    grep -oE 'istiod-(asm-[0-9]+-[0-9]+)' | \
    sed 's/istiod-//' | \
    head -n1)

if [[ -z "${ISTIO_VERSION}" ]]; then
    echo "  ⚠️  WARNING: Could not find Istio version in aks-istio-system namespace"
    echo "  Skipping Istio namespace labeling. Pods will not have Istio sidecars."
else
    echo "  Found Istio version: ${ISTIO_VERSION}"
    kubectl label namespace "${NAMESPACE}" "istio.io/rev=${ISTIO_VERSION}" --overwrite
    echo "  ✓ Namespace labeled with istio.io/rev=${ISTIO_VERSION}"
fi

# Delete existing ConfigMap if it exists
kubectl delete configmap resource-cleaner-scripts -n "${NAMESPACE}" --ignore-not-found=true

# Create new ConfigMap
kubectl create configmap resource-cleaner-scripts \
    --from-file="${RESOURCE_CLEANER_DIR}" \
    --namespace="${NAMESPACE}"

echo "  ✓ ConfigMap created from scripts in ${RESOURCE_CLEANER_DIR}"

echo ""

# Step 2: Deploy the CronJob
echo "Step 2: Deploying CronJob..."

# Use sed to replace variables in the template
sed -e "s/\${RESOURCE_CLEANER_NAMESPACE}/${NAMESPACE}/g" \
    -e "s/\${RESOURCE_CLEANER_CLUSTERROLE_NAME}/resource-cleaner/g" \
    -e "s|\${AZURE_CLI_IMAGE}|mcr.microsoft.com/azure-cli:2.78.0|g" \
    -e "s/\${AZURE_CLIENT_ID}/${AZURE_CLIENT_ID}/g" \
    -e "s/\${AZURE_TENANT_ID}/${AZURE_TENANT_ID}/g" \
    -e "s|\${MAESTRO_URL}|${MAESTRO_URL}|g" \
    -e "s/\${RETENTION_HOURS}/${RETENTION_HOURS}/g" \
    "${SCRIPT_DIR}/resource-cleaner.yaml" | kubectl apply -f -

echo "  ✓ CronJob deployed successfully"

echo ""
echo "========================================"
echo "✅ Deployment completed successfully!"
echo ""
echo "To verify the deployment:"
echo "  kubectl get cronjob -n ${NAMESPACE}"
echo "  kubectl get configmap resource-cleaner-scripts -n ${NAMESPACE}"
echo ""
echo "To manually trigger a job:"
echo "  kubectl create job -n ${NAMESPACE} manual-cleanup-\$(date +%s) --from=cronjob/resource-cleaner"
echo ""
echo "To view logs from the latest job:"
echo "  kubectl logs -n ${NAMESPACE} -l job-name=\$(kubectl get jobs -n ${NAMESPACE} --sort-by=.metadata.creationTimestamp -o name | tail -1 | cut -d/ -f2)"
echo ""
echo "To enable dry-run mode for the job (preview cleanup without deleting):"
echo "  kubectl set env cronjob/resource-cleaner -n ${NAMESPACE} DRY_RUN=true"
echo ""
echo "To disable dry-run mode:"
echo "  kubectl set env cronjob/resource-cleaner -n ${NAMESPACE} DRY_RUN=false"
echo "========================================"

