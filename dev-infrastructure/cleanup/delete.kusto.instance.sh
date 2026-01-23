#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: RESOURCE_GROUP=<resource-group-name> [DRY_RUN=true] ./cleanup-rg.sh
# 
# Deletes all specific Kusto instance
#
# Environment variables:
#   RESOURCE_GROUP - Name of the resource group to clean up (required)
#   KUSTO_INSTANCE - Name of the Kusto instance to clean up (required)
#   DRY_RUN        - Set to 'true' to preview actions without deleting (optional, default: false)

if [[ -z "${RESOURCE_GROUP:-}" ]]; then
    echo "Error: RESOURCE_GROUP environment variable is required"
    echo "Usage: RESOURCE_GROUP=<resource-group-name> [DRY_RUN=true] ./delete.kusto.instance.sh"
    exit 1
fi

if [[ -z "${KUSTO_INSTANCE:-}" ]]; then
    echo "Error: KUSTO_INSTANCE environment variable is required"
    echo "Usage: KUSTO_INSTANCE=<kusto-instance-name> [DRY_RUN=true] ./delete.kusto.instance.sh"
    exit 1
fi

DRY_RUN="${DRY_RUN:-false}"

echo "🧹 Starting deletion of Kusto instance: $KUSTO_INSTANCE"
if [[ "$DRY_RUN" == "true" ]]; then
    echo "🔍 DRY RUN MODE - No resources will actually be deleted"
fi

# Check if resource group exists
if [[ "$(az group exists --name "$RESOURCE_GROUP" --output tsv 2>/dev/null)" != "true" ]]; then
    echo "⚠️ Resource group '$RESOURCE_GROUP' does not exist"
    exit 0
fi

# Function to log actions
log() {
    local level="$1"
    shift
    local message="$*"
    case "$level" in
        INFO) echo "(i) $message" ;;
        WARN) echo "(w) $message" ;;
        ERROR) echo "(!) $message" ;;
        SUCCESS) echo "(o) $message" ;;
        STEP) echo "(~) $message" ;;
    esac
}

# Function to list all resources in the resource group for debugging
list_all_resources() {
    log INFO "Listing all resources in resource group '$RESOURCE_GROUP':"
    az resource list --resource-group "$RESOURCE_GROUP" \
        --query "[].{Name:name, Type:type, Id:id}" \
        --output table 2>/dev/null || log ERROR "Failed to list resources"
}

# List all resources for debugging
if [[ "$DRY_RUN" == "true" ]]; then
    list_all_resources
fi

# Function to check if resource has locks
has_locks() {
    local resource_id="$1"
    local lock_count
    lock_count=$(az lock list --resource "$resource_id" --query "length(@)" --output tsv 2>/dev/null || echo "0")
    [[ "$lock_count" -gt 0 ]]
}

# Function to get resource IDs by type
get_resources_by_type() {
    local resource_type="$1"
    az resource list --resource-group "$RESOURCE_GROUP" \
        --resource-type "$resource_type" \
        --query "[].id" \
        --output tsv 2>/dev/null || true
}

log STEP "Removing Kusto instance"
kusto_instance_id=$(get_resources_by_type "microsoft.kusto/clusters")
if [[ -n "$kusto_instance_id" ]]; then
    if has_locks "$kusto_instance_id"; then
        log WARN "Skipping locked Kusto instance: $kusto_instance_id"
        continue
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        log INFO "[DRY RUN] Would delete Kusto instance: $(basename "$kusto_instance_id")"
    else
        log INFO "Deleting Kusto instance: $(basename "$kusto_instance_id")"
        if az resource delete --yes --force --ids "$kusto_instance_id" --output none 2>/dev/null; then
            log SUCCESS "Deleted Kusto instance: $(basename "$kusto_instance_id")"
        else
            log ERROR "Failed to delete Kusto instance: $(basename "$kusto_instance_id")"
        fi
    fi
fi
