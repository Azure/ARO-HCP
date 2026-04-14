#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: RESOURCE_GROUP=<resource-group-name> KUSTO_INSTANCE=<kusto-instance-name> GRAFANA_RESOURCE_ID=<grafana-resource-id> [DRY_RUN=true] ./delete.kusto.instance.sh
# 
# Deletes a specific Kusto instance and its matching Grafana datasource
#
# Environment variables:
#   RESOURCE_GROUP      - Name of the resource group to clean up (required)
#   KUSTO_INSTANCE      - Name of the Kusto instance to clean up (required)
#   GRAFANA_RESOURCE_ID - Resource ID of the shared Grafana instance (required)
#   DRY_RUN             - Set to 'true' to preview actions without deleting (optional, default: false)

if [[ -z "${RESOURCE_GROUP:-}" ]]; then
    echo "Error: RESOURCE_GROUP environment variable is required"
    echo "Usage: RESOURCE_GROUP=<resource-group-name> KUSTO_INSTANCE=<kusto-instance-name> GRAFANA_RESOURCE_ID=<grafana-resource-id> [DRY_RUN=true] ./delete.kusto.instance.sh"
    exit 1
fi

if [[ -z "${KUSTO_INSTANCE:-}" ]]; then
    echo "Error: KUSTO_INSTANCE environment variable is required"
    echo "Usage: KUSTO_INSTANCE=<kusto-instance-name> GRAFANA_RESOURCE_ID=<grafana-resource-id> [DRY_RUN=true] ./delete.kusto.instance.sh"
    exit 1
fi

if [[ -z "${GRAFANA_RESOURCE_ID:-}" ]]; then
    echo "Error: GRAFANA_RESOURCE_ID environment variable is required"
    echo "Usage: RESOURCE_GROUP=<resource-group-name> KUSTO_INSTANCE=<kusto-instance-name> GRAFANA_RESOURCE_ID=<grafana-resource-id> [DRY_RUN=true] ./delete.kusto.instance.sh"
    exit 1
fi

DRY_RUN="${DRY_RUN:-false}"

echo "🧹 Starting deletion of Kusto instance: $KUSTO_INSTANCE"
if [[ "$DRY_RUN" == "true" ]]; then
    echo "🔍 DRY RUN MODE - No resources will actually be deleted"
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

# Function to check if resource has locks
has_locks() {
    local resource_id="$1"
    local lock_count
    lock_count=$(az lock list --resource "$resource_id" --query "length(@)" --output tsv 2>/dev/null || echo "0")
    [[ "$lock_count" -gt 0 ]]
}

# Function to get resource IDs by type
get_kusto_instance_id() {
    az resource list --resource-group "$RESOURCE_GROUP" \
        --resource-type "microsoft.kusto/clusters" \
        --name "$KUSTO_INSTANCE" \
        --query "[].id" \
        --output tsv 2>/dev/null || true
}

parse_grafana_context() {
    GRAFANA_SUBSCRIPTION_ID=$(printf '%s' "${GRAFANA_RESOURCE_ID}" | cut -d/ -f3)
    GRAFANA_RG=$(printf '%s' "${GRAFANA_RESOURCE_ID}" | cut -d/ -f5)
    GRAFANA_NAME=$(printf '%s' "${GRAFANA_RESOURCE_ID}" | cut -d/ -f9)
    ENVIRONMENT_NAME=$(printf '%s' "${GRAFANA_NAME}" | sed 's/^arohcp-//')
    EXPECTED_KUSTO_PREFIX="hcp-${ENVIRONMENT_NAME}-"

    case "${KUSTO_INSTANCE}" in
        "${EXPECTED_KUSTO_PREFIX}"*)
            GEO_SHORT_ID=${KUSTO_INSTANCE#"${EXPECTED_KUSTO_PREFIX}"}
            ;;
        *)
            log ERROR "Unexpected Kusto instance '${KUSTO_INSTANCE}', expected prefix '${EXPECTED_KUSTO_PREFIX}'"
            exit 1
            ;;
    esac

    DATASOURCE_NAME="kusto-${ENVIRONMENT_NAME}-${GEO_SHORT_ID}"
}

delete_grafana_datasource() {
    log STEP "Removing Grafana datasource"
    if ! az resource wait \
        --custom "properties.provisioningState=='Succeeded'" \
        --ids "${GRAFANA_RESOURCE_ID}" \
        --api-version 2024-10-01 \
        --timeout 300; then
        log ERROR "Failed waiting for Grafana resource '${GRAFANA_RESOURCE_ID}' to reach provisioningState 'Succeeded' within 300 seconds"
        exit 1
    fi

    local existing_datasource
    existing_datasource=$(az grafana data-source list \
        --name "${GRAFANA_NAME}" \
        --resource-group "${GRAFANA_RG}" \
        --subscription "${GRAFANA_SUBSCRIPTION_ID}" \
        --query "[?name=='${DATASOURCE_NAME}'].name | [0]" \
        --output tsv)

    if [[ -z "${existing_datasource}" ]]; then
        log INFO "Grafana datasource '${DATASOURCE_NAME}' not found, nothing to delete"
        return 0
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        log INFO "[DRY RUN] Would delete Grafana datasource: ${DATASOURCE_NAME}"
        return 0
    fi

    log INFO "Deleting Grafana datasource: ${DATASOURCE_NAME}"
    az grafana data-source delete \
        --name "${GRAFANA_NAME}" \
        --resource-group "${GRAFANA_RG}" \
        --subscription "${GRAFANA_SUBSCRIPTION_ID}" \
        --data-source "${DATASOURCE_NAME}" \
        --output none
    log SUCCESS "Deleted Grafana datasource: ${DATASOURCE_NAME}"
}

parse_grafana_context

resource_group_exists=$(az group exists --name "$RESOURCE_GROUP" --output tsv 2>/dev/null || echo "false")

# List all resources for debugging
if [[ "$DRY_RUN" == "true" && "$resource_group_exists" == "true" ]]; then
    list_all_resources
fi

if [[ "$resource_group_exists" == "true" ]]; then
    log STEP "Removing Kusto instance"
    kusto_instance_id=$(get_kusto_instance_id)
    if [[ -n "$kusto_instance_id" ]]; then
        if has_locks "$kusto_instance_id"; then
            log WARN "Skipping locked Kusto instance: $kusto_instance_id"
            exit 0
        fi

        if [[ "$DRY_RUN" == "true" ]]; then
            log INFO "[DRY RUN] Would delete Kusto instance: $(basename "$kusto_instance_id")"
        else
            log INFO "Deleting Kusto instance: $(basename "$kusto_instance_id")"
            if az resource delete --ids "$kusto_instance_id" --output none 2>/dev/null; then
                log SUCCESS "Deleted Kusto instance: $(basename "$kusto_instance_id")"
            else
                log ERROR "Failed to delete Kusto instance: $(basename "$kusto_instance_id")"
            fi
        fi
    else
        log WARN "Kusto instance '$KUSTO_INSTANCE' not found in resource group '$RESOURCE_GROUP', continuing with datasource cleanup"
    fi
else
    log WARN "Resource group '$RESOURCE_GROUP' does not exist, skipping Kusto instance deletion"
fi

delete_grafana_datasource
