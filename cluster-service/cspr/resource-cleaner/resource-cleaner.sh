#!/bin/bash
set -euo pipefail

###############################################################################
# Resource Cleaner Main Script
#
# This script orchestrates the cleanup of leftover resources from E2E tests
# and other operations. It coordinates multiple cleanup scripts, passing a
# consistent timestamp to all of them.
#
# Prerequisites:
# - kubectl configured with access to the cluster
# - az CLI configured with appropriate Azure credentials
# - jq installed for JSON parsing
#
# Usage:
#   ./resource-cleaner.sh [RETENTION_HOURS] [--dry-run] [--maestro-url <url>]
#
# Arguments:
#   RETENTION_HOURS       - Hours to retain resources (default: 3)
#   --dry-run             - Preview what would be deleted without actually deleting
#   --maestro-url <url>   - Maestro API URL (default: http://localhost:8002)
###############################################################################

# store ts=now()
# get the list of cluster ids from the leftover hosted cluster crs
# delete all maestro bundles (older than 3 hours from ts)
# wait for all of them to be deleted
# delete all the managed resource groups with prefix "e2e_tests_mrg_name" (older than 3 hours from ts)
# delete the customer resource groups with prefix "pr-check-e2e-tests-resource-group-" (older than 3 hours from ts)
# delete MI secrets under ah-cspr-mi-usw3-1 keyvalut (older than ts-3h)
# delete OIDC signing token secrets under ah-cspr-cx-usw3-1 keyvalut (older than ts-3h)
# delete certificates under ah-cspr-cx-usw3-1 keyvalut (older than ts-3h)
# for each cluster id delete the acr token from 'arohcpocpdev' container registry (https://portal.azure.com/#@redhat0.onmicrosoft.com/resource/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/global/providers/Microsoft.ContainerRegistry/registries/arohcpocpdev/token)


SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Parse arguments
RETENTION_HOURS=3
DRY_RUN=false
# MAESTRO_URL is set in common.sh, but can be overridden here

while [[ $# -gt 0 ]]; do
    case $1 in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --maestro-url)
            MAESTRO_URL="$2"
            shift 2
            ;;
        *)
            RETENTION_HOURS=$1
            shift
            ;;
    esac
done

# Store current timestamp and calculate cutoff time
CURRENT_TIME=$(date +%s)
CUTOFF_TIME=$((CURRENT_TIME - RETENTION_HOURS * 3600))
CUTOFF_DATE=$(date -u -d "@${CUTOFF_TIME}" +"%Y-%m-%dT%H:%M:%SZ")

log_info "================================================"
if [[ "${DRY_RUN}" == "true" ]]; then
    log_info "🔍 DRY RUN MODE - No resources will be deleted"
fi
log_info "Starting resource cleanup at $(date -u)"
log_info "Retention period: ${RETENTION_HOURS} hours"
log_info "Cutoff time: ${CUTOFF_DATE}"
log_info "Maestro URL: ${MAESTRO_URL}"
log_info "================================================"

# Set Azure subscription context once at the beginning
log_info "Setting Azure subscription context..."
if az account set --subscription "${ARO_HCP_DEV_SUBSCRIPTION}" &>/dev/null; then
    log_info "✓ Azure subscription set to: ${ARO_HCP_DEV_SUBSCRIPTION}"
else
    log_error "Failed to set Azure subscription '${ARO_HCP_DEV_SUBSCRIPTION}'"
    log_error "Resource cleanup cannot proceed without proper Azure subscription context"
    exit 1
fi

# Array to track failed steps
FAILED_SCRIPTS=()

###############################################################################
# Execute cleanup steps
###############################################################################

# Step 1: Get cluster IDs
log_info ""
log_info "Step 1: Retrieving cluster IDs..."
log_info "----------------------------------------"
if "${SCRIPT_DIR}/retrieve-cluster-ids.sh" "${CUTOFF_TIME}"; then
    log_info "✓ Cluster ID retrieval completed"
else
    log_error "✗ Cluster ID retrieval failed"
    FAILED_SCRIPTS+=("retrieve-cluster-ids.sh")
fi

# Step 2: Delete maestro bundles
log_info ""
log_info "Step 2: Deleting maestro bundles..."
log_info "----------------------------------------"
# Convert CUTOFF_TIME to Maestro date format (ISO 8601 with microseconds)
CUTOFF_DATE=$(date -u -d "@${CUTOFF_TIME}" '+%Y-%m-%dT%H:%M:%S.000000Z')
if "${SCRIPT_DIR}/cleanup-bundles.sh" "${MAESTRO_URL}" "${CUTOFF_DATE}" "${DRY_RUN}"; then
    log_info "✓ Bundle cleanup completed"
else
    log_error "✗ Bundle cleanup failed"
    FAILED_SCRIPTS+=("cleanup-bundles.sh")
fi

# Step 3: Delete managed resource groups
log_info ""
log_info "Step 3: Deleting managed resource groups..."
log_info "----------------------------------------"
if "${SCRIPT_DIR}/delete-resource-groups.sh" "${CUTOFF_TIME}" "${MANAGED_RG_PREFIX}" "${DRY_RUN}"; then
    log_info "✓ Managed resource group cleanup completed"
else
    log_error "✗ Managed resource group cleanup failed"
    FAILED_SCRIPTS+=("delete-resource-groups.sh (${MANAGED_RG_PREFIX})")
fi

# Step 4: Delete customer resource groups
log_info ""
log_info "Step 4: Deleting customer resource groups..."
log_info "----------------------------------------"
if "${SCRIPT_DIR}/delete-resource-groups.sh" "${CUTOFF_TIME}" "${CUSTOMER_RG_PREFIX}" "${DRY_RUN}"; then
    log_info "✓ Customer resource group cleanup completed"
else
    log_error "✗ Customer resource group cleanup failed"
    FAILED_SCRIPTS+=("delete-resource-groups.sh (${CUSTOMER_RG_PREFIX})")
fi

# Step 5: Delete keyvault secrets from MI keyvault
log_info ""
log_info "Step 5: Cleaning up MI keyvault secrets (${MI_KEYVAULT})..."
log_info "----------------------------------------"
MI_SECRETS_CMD_ARGS=("--vault-name" "${MI_KEYVAULT}" "--cutoff-time" "${CUTOFF_TIME}")
if [[ "${DRY_RUN}" == "true" ]]; then
    MI_SECRETS_CMD_ARGS+=("--dry-run")
fi
if "${SCRIPT_DIR}/delete-keyvault-secrets.sh" "${MI_SECRETS_CMD_ARGS[@]}"; then
    log_info "✓ MI keyvault secrets cleanup completed"
else
    log_error "✗ MI keyvault secrets cleanup failed"
    FAILED_SCRIPTS+=("delete-keyvault-secrets.sh (${MI_KEYVAULT})")
fi

# Step 6: Delete keyvault secrets from CX keyvault
log_info ""
log_info "Step 6: Cleaning up CX keyvault secrets (${CX_KEYVAULT})..."
log_info "----------------------------------------"
CX_SECRETS_CMD_ARGS=("--vault-name" "${CX_KEYVAULT}" "--cutoff-time" "${CUTOFF_TIME}")
if [[ "${DRY_RUN}" == "true" ]]; then
    CX_SECRETS_CMD_ARGS+=("--dry-run")
fi
if "${SCRIPT_DIR}/delete-keyvault-secrets.sh" "${CX_SECRETS_CMD_ARGS[@]}"; then
    log_info "✓ CX keyvault secrets cleanup completed"
else
    log_error "✗ CX keyvault secrets cleanup failed"
    FAILED_SCRIPTS+=("delete-keyvault-secrets.sh (${CX_KEYVAULT})")
fi

# Step 7: Delete certificates from CX keyvault
log_info ""
log_info "Step 7: Deleting certificates from CX keyvault (${CX_KEYVAULT})..."
log_info "----------------------------------------"
CERT_CMD_ARGS=("--vault-name" "${CX_KEYVAULT}" "--cutoff-time" "${CUTOFF_TIME}")
if [[ "${DRY_RUN}" == "true" ]]; then
    CERT_CMD_ARGS+=("--dry-run")
fi
if "${SCRIPT_DIR}/delete-keyvault-certificates.sh" "${CERT_CMD_ARGS[@]}"; then
    log_info "✓ CX keyvault certificates cleanup completed"
else
    log_error "✗ CX keyvault certificates cleanup failed"
    FAILED_SCRIPTS+=("delete-keyvault-certificates.sh (${CX_KEYVAULT})")
fi

# Step 8: Delete ACR tokens
log_info ""
log_info "Step 8: Deleting ACR tokens..."
log_info "----------------------------------------"
if "${SCRIPT_DIR}/cleanup-acr-tokens.sh" "${DRY_RUN}"; then
    log_info "✓ ACR token cleanup completed"
else
    log_error "✗ ACR token cleanup failed"
    FAILED_SCRIPTS+=("cleanup-acr-tokens.sh")
fi

###############################################################################
# Cleanup and Summary
###############################################################################

# Cleanup temporary files
CLUSTER_IDS_FILE="${SCRIPT_DIR}/.cluster_ids.tmp"
if [[ -f "${CLUSTER_IDS_FILE}" ]]; then
    rm -f "${CLUSTER_IDS_FILE}"
fi

log_info ""
log_info "================================================"
log_info "Resource cleanup completed at $(date -u)"
log_info "================================================"

if [[ "${DRY_RUN}" == "true" ]]; then
    log_info ""
    log_info "🔍 DRY RUN COMPLETE - No resources were actually deleted"
fi

if [[ ${#FAILED_SCRIPTS[@]} -eq 0 ]]; then
    log_info "✅ All cleanup steps completed successfully!"
    exit 0
else
    log_error "❌ The following steps failed:"
    for script in "${FAILED_SCRIPTS[@]}"; do
        log_error "  - ${script}"
    done
    exit 1
fi
