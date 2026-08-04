#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: RESOURCE_GROUP=<resource-group-name> FUNCTION_APP_NAME=<name> STORAGE_ACCOUNT_NAME=<name>
#        APP_SERVICE_PLAN_NAME=<name> MANAGED_IDENTITY_NAME=<name> [DRY_RUN=true] ./cleanup.sh
#
# Deletes all Geneva Health Function App resources from a resource group.
#
# Environment variables:
#   RESOURCE_GROUP         - Resource group containing the Function App resources (required)
#   FUNCTION_APP_NAME      - Name of the Function App (required)
#   STORAGE_ACCOUNT_NAME   - Name of the Storage Account (required)
#   APP_SERVICE_PLAN_NAME  - Name of the App Service Plan (required)
#   MANAGED_IDENTITY_NAME  - Name of the User-Assigned Managed Identity (required)
#   DRY_RUN                - Set to 'true' to preview actions without deleting (optional, default: false)

for var in RESOURCE_GROUP FUNCTION_APP_NAME STORAGE_ACCOUNT_NAME APP_SERVICE_PLAN_NAME MANAGED_IDENTITY_NAME; do
    if [[ -z "${!var:-}" ]]; then
        echo "Error: $var environment variable is required"
        exit 1
    fi
done

DRY_RUN="${DRY_RUN:-false}"

echo "Starting cleanup of Geneva Health Function resources in: $RESOURCE_GROUP"
if [[ "$DRY_RUN" == "true" ]]; then
    echo "DRY RUN MODE - No resources will actually be deleted"
fi

if [[ "$(az group exists --name "$RESOURCE_GROUP" --output tsv 2>/dev/null)" != "true" ]]; then
    echo "Resource group '$RESOURCE_GROUP' does not exist, nothing to clean up"
    exit 0
fi

delete_resource() {
    local resource_type="$1"
    local resource_name="$2"

    local resource_id
    resource_id=$(az resource list \
        --resource-group "$RESOURCE_GROUP" \
        --resource-type "$resource_type" \
        --name "$resource_name" \
        --query "[].id" --output tsv 2>/dev/null || true)

    if [[ -z "$resource_id" ]]; then
        echo "  Not found: $resource_name ($resource_type) - skipping"
        return 0
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        echo "  [DRY RUN] Would delete: $resource_name ($resource_type)"
    else
        echo "  Deleting: $resource_name ($resource_type)"
        if az resource delete --ids "$resource_id" --output none 2>/dev/null; then
            echo "  Deleted: $resource_name"
        else
            echo "  Failed to delete: $resource_name"
            return 1
        fi
    fi
}

echo "Step 1: Deleting Function App"
delete_resource "Microsoft.Web/sites" "$FUNCTION_APP_NAME"

echo "Step 2: Deleting App Service Plan"
delete_resource "Microsoft.Web/serverfarms" "$APP_SERVICE_PLAN_NAME"

echo "Step 3: Deleting Storage Account"
delete_resource "Microsoft.Storage/storageAccounts" "$STORAGE_ACCOUNT_NAME"

echo "Step 4: Deleting Managed Identity"
delete_resource "Microsoft.ManagedIdentity/userAssignedIdentities" "$MANAGED_IDENTITY_NAME"

echo "Cleanup completed for Geneva Health Function resources in: $RESOURCE_GROUP"
