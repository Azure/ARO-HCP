#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: RESOURCE_GROUP=<resource-group-name> [DRY_RUN=true] ./cleanup-rg.sh
# 
# Deletes all resources in a resource group except those with locks.
# Handles dependencies by deleting resources in the proper order:
# 1. Remove NSP associations first
# 2. Delete private endpoints and connections  
# 3. Delete application-level resources
# 4. Delete infrastructure resources
# 5. Delete fundamental resources (networks, storage, etc.)
#
# Environment variables:
#   RESOURCE_GROUP - Name of the resource group to clean up (required)
#   DRY_RUN        - Set to 'true' to preview actions without deleting (optional, default: false)

if [[ -z "${RESOURCE_GROUP:-}" ]]; then
    echo "Error: RESOURCE_GROUP environment variable is required"
    echo "Usage: RESOURCE_GROUP=<resource-group-name> [DRY_RUN=true] ./cleanup-rg.sh"
    exit 1
fi

DRY_RUN="${DRY_RUN:-false}"

echo "🧹 Starting cleanup of resource group: $RESOURCE_GROUP"
if [[ "$DRY_RUN" == "true" ]]; then
    echo "🔍 DRY RUN MODE - No resources will actually be deleted"
fi

# Check if resource group exists
if ! az group exists --name "$RESOURCE_GROUP" --output tsv >/dev/null 2>&1; then
    echo "❌ Resource group '$RESOURCE_GROUP' does not exist"
    exit 1
fi

# Function to log actions
log() {
    local level="$1"
    shift
    local message="$*"
    case "$level" in
        INFO) echo "ℹ️  $message" ;;
        WARN) echo "⚠️  $message" ;;
        ERROR) echo "❌ $message" ;;
        SUCCESS) echo "✅ $message" ;;
        STEP) echo "🔄 $message" ;;
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

# Function to safely delete resources with error handling
safe_delete() {
    local resource_ids="$1"
    local description="$2"

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

        if [[ "$DRY_RUN" == "true" ]]; then
            log INFO "[DRY RUN] Would delete: $resource_name"
        else
            log INFO "Deleting: $resource_name"
            if az resource delete --ids "$resource_id" --output none 2>/dev/null; then
                log SUCCESS "Deleted: $resource_name"
            else
                log ERROR "Failed to delete: $resource_name"
            fi
        fi
    done <<< "$resource_ids"
}

# Function to get resource IDs by type
get_resources_by_type() {
    local resource_type="$1"
    az resource list --resource-group "$RESOURCE_GROUP" \
        --resource-type "$resource_type" \
        --query "[].id" \
        --output tsv 2>/dev/null || true
}

# Function to get resources by multiple types (OR condition)
get_resources_by_types() {
    local types=("$@")
    local all_resources=""
    local type
    local resources

    for type in "${types[@]}"; do
        resources=$(get_resources_by_type "$type")
        if [[ -n "$resources" ]]; then
            if [[ -n "$all_resources" ]]; then
                all_resources+=$'\n'
            fi
            all_resources+="$resources"
        fi
    done

    echo "$all_resources"
}

# Step 1: Remove NSP (Network Security Perimeter) associations first
# This prevents dependency issues when deleting associated resources
log STEP "Step 1: Removing NSP associations"
nsp_ids=$(get_resources_by_type "Microsoft.Network/networkSecurityPerimeters")
if [[ -n "$nsp_ids" ]]; then
    while IFS= read -r nsp_id; do
        [[ -z "$nsp_id" ]] && continue

        if has_locks "$nsp_id"; then
            log WARN "Skipping locked NSP: $nsp_id"
            continue
        fi

        if [[ "$DRY_RUN" == "true" ]]; then
            log INFO "[DRY RUN] Would delete NSP and associations: $(basename "$nsp_id")"
        else
            log INFO "Deleting NSP and associations: $(basename "$nsp_id")"
            if az network perimeter delete --yes --force --ids "$nsp_id" --output none 2>/dev/null; then
                log SUCCESS "Deleted NSP: $(basename "$nsp_id")"
            else
                log ERROR "Failed to delete NSP: $(basename "$nsp_id")"
            fi
        fi
    done <<< "$nsp_ids"
else
    log INFO "No NSPs found"
fi

# Step 2: Delete private endpoints and private link connections
# These often have dependencies on other resources and should be deleted early
log STEP "Step 2: Deleting private endpoints and connections"
private_endpoints=$(get_resources_by_types \
    "Microsoft.Network/privateEndpoints" \
    "Microsoft.Network/privateLinkServices" \
    "Microsoft.Network/privateEndpointConnections" \
    "Microsoft.Network/privateDnsZones"
)
safe_delete "$private_endpoints" "private endpoints and connections"


# Step 4: Clean up any remaining resources
log STEP "Step 3: Cleaning up remaining resources"
remaining_resources=$(az resource list --resource-group "$RESOURCE_GROUP" --query "[].id" --output tsv 2>/dev/null || true)
if [[ -n "$remaining_resources" ]]; then
    log INFO "Found remaining resources, attempting to delete..."
    safe_delete "$remaining_resources" "remaining resources"
else
    log INFO "No remaining resources found"
fi

# Final summary

log STEP "Cleanup completed for resource group: $RESOURCE_GROUP"

# Check what's left
remaining_count=$(az resource list --resource-group "$RESOURCE_GROUP" --query "length(@)" --output tsv 2>/dev/null || echo "0")
locked_count=$(az resource list --resource-group "$RESOURCE_GROUP" --query "length([?locks])" --output tsv 2>/dev/null || echo "0")

if [[ "$remaining_count" -eq 0 ]]; then
    log SUCCESS "All resources have been deleted from the resource group"
    if [[ "$DRY_RUN" == "false" ]]; then
        log INFO "Executing Safe delete of resource group $RESOURCE_GROUP"
        az group delete --name $RESOURCE_GROUP --yes
    fi
elif [[ "$locked_count" -gt 0 ]]; then
    log INFO "Resource group cleanup completed. $remaining_count resources remain (including $locked_count locked resources)"
else
    log WARN "Resource group cleanup completed. $remaining_count resources remain"
    log INFO "Some resources may have failed to delete due to dependencies or errors"
fi