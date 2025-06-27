#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: RESOURCE_GROUP=<resource-group-name> [DRY_RUN=true] ./cleanup-rg.sh
# 
# Deletes all resources in a resource group except those with locks.
# Handles dependencies by deleting resources in the proper order:
# 1. Remove NSP associations first
# 2. Delete private endpoints and DNS components (in dependency order)
# 3. Delete application and infrastructure resources (excluding VNETs/NSGs/DCRs/DCEs) - includes AKS clusters
# 4. Delete Data Collection Rules (DCRs) and Endpoints (DCEs) after AKS clusters are deleted
# 5. Delete Virtual Networks (to clean up subnet references to NSGs)
# 6. Delete Network Security Groups (after VNETs to ensure no subnet references remain)
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
fi

# Step 2: Delete private endpoints and DNS zone components in correct dependency order
# Private DNS zones have complex dependencies that require careful ordering
#
# DEPENDENCY ISSUE EXPLANATION:
# Private DNS zones often fail to delete on the first attempt because of these dependencies:
# 1. privateDnsZoneGroups (link private endpoints to DNS zones)
# 2. privateEndpointConnections (connect endpoints to services)
# 3. privateEndpoints (the actual endpoints)
# 4. virtualNetworkLinks (link DNS zones to VNets)
# 5. privateLinkServices (the backend services)
# 6. privateDnsZones (the DNS zones themselves)
#
# Azure's eventual consistency model means these resources may not appear
# as "ready for deletion" immediately, requiring retries for DNS zones.
#
log STEP "Step 2: Deleting private endpoints and DNS components"

# Step 2a: Remove private DNS zone groups first (these link private endpoints to DNS zones)
log STEP "Step 2a: Removing private DNS zone groups"
dns_zone_groups=$(az resource list --resource-group "$RESOURCE_GROUP" \
    --resource-type "Microsoft.Network/privateEndpoints/privateDnsZoneGroups" \
    --query "[].id" \
    --output tsv 2>/dev/null || true)
safe_delete "$dns_zone_groups" "private DNS zone groups"

# Step 2b: Delete private endpoint connections
log STEP "Step 2b: Deleting private endpoint connections"
private_endpoint_connections=$(get_resources_by_type "Microsoft.Network/privateEndpointConnections")
safe_delete "$private_endpoint_connections" "private endpoint connections"

# Step 2c: Delete private endpoints themselves
log STEP "Step 2c: Deleting private endpoints"
private_endpoints=$(get_resources_by_type "Microsoft.Network/privateEndpoints")
safe_delete "$private_endpoints" "private endpoints"

# Step 2d: Delete virtual network links from private DNS zones
# These must be removed before the DNS zones themselves can be deleted
log STEP "Step 2d: Removing virtual network links from private DNS zones"
vnet_links=$(az resource list --resource-group "$RESOURCE_GROUP" \
    --resource-type "Microsoft.Network/privateDnsZones/virtualNetworkLinks" \
    --query "[].id" \
    --output tsv 2>/dev/null || true)
safe_delete "$vnet_links" "private DNS zone virtual network links"

# Step 2e: Delete private link services
log STEP "Step 2e: Deleting private link services"
private_link_services=$(get_resources_by_type "Microsoft.Network/privateLinkServices")
safe_delete "$private_link_services" "private link services"

# Step 2f: Finally delete private DNS zones (now that all dependencies are removed)
log STEP "Step 2f: Deleting private DNS zones"
private_dns_zones=$(get_resources_by_type "Microsoft.Network/privateDnsZones")
safe_delete "$private_dns_zones" "private DNS zones" 3  # Use 3 retries for DNS zones

# Verify private DNS zones are deleted
remaining_dns_zones=$(get_resources_by_type "Microsoft.Network/privateDnsZones")
if [[ -n "$remaining_dns_zones" ]] && [[ "$DRY_RUN" != "true" ]]; then
    log WARN "Some private DNS zones still remain after cleanup attempts:"
    while IFS= read -r zone_id; do
        [[ -z "$zone_id" ]] && continue
        log WARN "  - $(basename "$zone_id")"
    done <<< "$remaining_dns_zones"
    log INFO "These may require manual intervention or additional cleanup cycles"
fi


# Step 3: Delete remaining application and infrastructure resources (excluding VNETs/NSGs and DCRs/DCEs)
# These resources can be safely deleted after handling private networking
log STEP "Step 3: Deleting application and infrastructure resources (excluding networking and monitoring)"
all_resources=$(az resource list --resource-group "$RESOURCE_GROUP" --query "[].id" --output tsv 2>/dev/null || true)
non_network_resources=""
while IFS= read -r resource_id; do
    [[ -z "$resource_id" ]] && continue
    # Skip VNETs, NSGs, DCRs, and DCEs - these will be handled in later steps
    if [[ "$resource_id" != *"/Microsoft.Network/virtualNetworks/"* ]] && \
       [[ "$resource_id" != *"/Microsoft.Network/networkSecurityGroups/"* ]] && \
       [[ "$resource_id" != *"/Microsoft.Insights/dataCollectionRules/"* ]] && \
       [[ "$resource_id" != *"/Microsoft.Insights/dataCollectionEndpoints/"* ]]; then
        if [[ -n "$non_network_resources" ]]; then
            non_network_resources+=$'\n'
        fi
        non_network_resources+="$resource_id"
    fi
done <<< "$all_resources"

if [[ -n "$non_network_resources" ]]; then
    safe_delete "$non_network_resources" "application and infrastructure resources"
else
    log INFO "No non-networking/monitoring resources found"
fi

# Step 4: Delete Data Collection Rules (DCRs) and Endpoints (DCEs) after AKS clusters are gone
# Note: DCR Associations are not standalone resources - they're properties of other resources
# and should be cleaned up when the associated resources (like AKS clusters) are deleted
log STEP "Step 4a: Deleting Data Collection Rules"
dcrs=$(get_resources_by_type "Microsoft.Insights/dataCollectionRules")
safe_delete "$dcrs" "Data Collection Rules" 3

log STEP "Step 4b: Deleting Data Collection Endpoints"
dces=$(get_resources_by_type "Microsoft.Insights/dataCollectionEndpoints")
safe_delete "$dces" "Data Collection Endpoints" 3

# Step 5: Delete Virtual Networks
# VNETs must be deleted before NSGs since subnets may reference NSGs
log STEP "Step 5: Deleting Virtual Networks"
vnets=$(get_resources_by_type "Microsoft.Network/virtualNetworks")
safe_delete "$vnets" "Virtual Networks"

# Step 6: Delete Network Security Groups
# NSGs are deleted after VNETs to ensure subnet references are cleaned up
log STEP "Step 6: Deleting Network Security Groups"
nsgs=$(get_resources_by_type "Microsoft.Network/networkSecurityGroups")
safe_delete "$nsgs" "Network Security Groups"

# Final summary

log STEP "Cleanup completed for resource group: $RESOURCE_GROUP"

# Check what's left
remaining_count=$(az resource list --resource-group "$RESOURCE_GROUP" --query "length(@)" --output tsv 2>/dev/null || echo "0")
locked_count=$(az resource list --resource-group "$RESOURCE_GROUP" --query "length([?locks])" --output tsv 2>/dev/null || echo "0")

if [[ "$remaining_count" -eq 0 ]]; then
    log SUCCESS "All resources have been deleted from the resource group"
    if [[ "$DRY_RUN" == "false" ]]; then
        log INFO "Executing Safe delete of resource group $RESOURCE_GROUP"
        az group delete --name "$RESOURCE_GROUP" --yes
    fi
elif [[ "$locked_count" -gt 0 ]]; then
    log INFO "Resource group cleanup completed. $remaining_count resources remain (including $locked_count locked resources)"
else
    log WARN "Resource group cleanup completed. $remaining_count resources remain"
    log INFO "Some resources may have failed to delete due to dependencies or errors"
fi
