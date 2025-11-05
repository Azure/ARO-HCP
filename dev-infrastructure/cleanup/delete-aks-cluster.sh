#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: RESOURCE_GROUP=<resource-group-name> [DRY_RUN=true] ./delete-aks-cluster.sh
#
# Deletes all aks clusters in a resource group
#
# Environment variables:
#   RESOURCE_GROUP - Name of the resource group to clean up (required)
#   DRY_RUN        - Set to 'true' to preview actions without deleting (optional, default: false)

if [[ -z "${RESOURCE_GROUP:-}" ]]; then
    echo "Error: RESOURCE_GROUP environment variable is required"
    echo "Usage: RESOURCE_GROUP=<resource-group-name> [DRY_RUN=true] ./delete-aks-cluster.sh"
    exit 1
fi

DRY_RUN="${DRY_RUN:-false}"

echo "🧹 Starting cleanup of resource group: $RESOURCE_GROUP"
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
        --resource-type Microsoft.ContainerService/managedClusters \
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

# Function to safely delete resources with error handling and retries
safe_delete() {
    local resource_ids="$1"
    local description="$2"
    local max_retries="${3:-1}"

    if [[ -z "$resource_ids" ]]; then
        return 0
    fi

    log STEP "Deleting $description..."

    while IFS= read -r resource_id; do
        [[ -z "$resource_id" ]] && continue

        if has_locks "$resource_id"; then
            log WARN "Skipping locked resource: $resource_id"
            continue
        fi

        local resource_name
        resource_name=$(basename "$resource_id")
        local resource_type
        resource_type=$(echo "$resource_id" | grep -o '/Microsoft\.[^/]*/[^/]*' | sed 's|^/||' || echo "Unknown")

        if [[ "$DRY_RUN" == "true" ]]; then
            log INFO "[DRY RUN] Would delete: $resource_name ($resource_type)"
        else
            log INFO "Deleting: $resource_name ($resource_type)"

            local attempt=1
            while [[ $attempt -le $max_retries ]]; do
                if [[ $attempt -gt 1 ]]; then
                    log INFO "Retry attempt $attempt for: $resource_name ($resource_type)"
                    sleep 10  # Wait between retries
                fi

                if az resource delete --ids "$resource_id" --output none 2>/dev/null; then
                    log SUCCESS "Deleted: $resource_name ($resource_type)"
                    break
                elif [[ $attempt -eq $max_retries ]]; then
                    log ERROR "Failed to delete after $max_retries attempts: $resource_name ($resource_type)"
                else
                    log WARN "Attempt $attempt failed for: $resource_name ($resource_type) (retrying...)"
                fi

                ((attempt++))
            done
        fi
    done <<< "$resource_ids"
}


# delete aks cluster
log STEP "Step 1: Deleting AKS cluster"
aks_resource_ids=$(az resource list --resource-group "$RESOURCE_GROUP" \
    --resource-type Microsoft.ContainerService/managedClusters \
    --query "[].id" --output tsv 2>/dev/null)
safe_delete "$aks_resource_ids" "AKS clusters" 3


log STEP "Cleanup completed for resource group: $RESOURCE_GROUP"

