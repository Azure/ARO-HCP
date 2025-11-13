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
#   --azure-client-id <id>     Azure Workload Identity Client ID (required)
#   --azure-tenant-id <id>     Azure Tenant ID (required)
#   --maestro-url <url>        Maestro API URL (default: http://maestro.maestro.svc.cluster.local:8000)
#   --retention-hours <hours>  Retention period in hours (default: 3)
#   --help                     Show this help message
###############################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESOURCE_CLEANER_DIR="${SCRIPT_DIR}/resource-cleaner"

# Default values
AZURE_CLIENT_ID=""
AZURE_TENANT_ID=""
MAESTRO_URL="http://maestro.maestro.svc.cluster.local:8000"
RETENTION_HOURS="3"
NAMESPACE="resource-cleaner"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --azure-client-id)
            AZURE_CLIENT_ID="$2"
            shift 2
            ;;
        --azure-tenant-id)
            AZURE_TENANT_ID="$2"
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
            head -n 20 "$0" | grep "^#" | sed 's/^# //' | sed 's/^#//'
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
if [[ -z "${AZURE_CLIENT_ID}" ]]; then
    echo "ERROR: --azure-client-id is required"
    echo "Use --help for usage information"
    exit 1
fi

if [[ -z "${AZURE_TENANT_ID}" ]]; then
    echo "ERROR: --azure-tenant-id is required"
    echo "Use --help for usage information"
    exit 1
fi

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
echo "Deployment Configuration:"
echo "  Azure Client ID: ${AZURE_CLIENT_ID}"
echo "  Azure Tenant ID: ${AZURE_TENANT_ID}"
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

