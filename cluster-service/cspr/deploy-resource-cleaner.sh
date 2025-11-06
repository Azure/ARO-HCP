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
#   --maestro-url <url>        Maestro API URL (default: http://maestro.maestro.svc.cluster.local:8000)
#   --retention-hours <hours>  Retention period in hours (default: 3)
#   --help                     Show this help message
###############################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESOURCE_CLEANER_DIR="${SCRIPT_DIR}/resource-cleaner"

# Default values
AZURE_CLIENT_ID="4579fe55-83eb-45a5-ba5e-ca90ffadd763"
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
echo "  Maestro URL: ${MAESTRO_URL}"
echo "  Retention Hours: ${RETENTION_HOURS}"
echo "  Namespace: ${NAMESPACE}"
echo ""

# Step 1: Create or update ConfigMap from script files
echo "Step 1: Creating ConfigMap from script files..."

# Create namespace if it doesn't exist
oc create namespace "${NAMESPACE}" --dry-run=client -o yaml | oc apply -f -

# Delete existing ConfigMap if it exists
oc delete configmap resource-cleaner-scripts -n "${NAMESPACE}" --ignore-not-found=true

# Create new ConfigMap
oc create configmap resource-cleaner-scripts \
    --from-file="${RESOURCE_CLEANER_DIR}" \
    --namespace="${NAMESPACE}"

echo "  ✓ ConfigMap created from scripts in ${RESOURCE_CLEANER_DIR}"

echo ""

# Step 2: Deploy the template
echo "Step 2: Deploying CronJob template..."

oc process -f "${SCRIPT_DIR}/resource-cleaner.yaml" \
    -p AZURE_CLIENT_ID="${AZURE_CLIENT_ID}" \
    -p MAESTRO_URL="${MAESTRO_URL}" \
    -p RETENTION_HOURS="${RETENTION_HOURS}" \
    | oc apply -f -

echo "  ✓ CronJob deployed successfully"

echo ""
echo "========================================"
echo "✅ Deployment completed successfully!"
echo ""
echo "To verify the deployment:"
echo "  oc get cronjob -n ${NAMESPACE}"
echo "  oc get configmap resource-cleaner-scripts -n ${NAMESPACE}"
echo ""
echo "To manually trigger a job:"
echo "  oc create job -n ${NAMESPACE} manual-cleanup-\$(date +%s) --from=cronjob/resource-cleaner"
echo ""
echo "To view logs from the latest job:"
echo "  oc logs -n ${NAMESPACE} -l job-name=\$(oc get jobs -n ${NAMESPACE} --sort-by=.metadata.creationTimestamp -o name | tail -1 | cut -d/ -f2)"
echo ""
echo "To enable dry-run mode for the job (preview cleanup without deleting):"
echo "  oc set env cronjob/resource-cleaner -n ${NAMESPACE} DRY_RUN=true"
echo ""
echo "To disable dry-run mode:"
echo "  oc set env cronjob/resource-cleaner -n ${NAMESPACE} DRY_RUN=false"
echo "========================================"

