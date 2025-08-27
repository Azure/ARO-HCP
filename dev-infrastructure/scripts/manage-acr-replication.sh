#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Function to display usage
usage() {
    echo "Usage: Set environment variables and run the script"
    echo ""
    echo "Required environment variables:"
    echo "  ACR_NAME: Name of the Azure Container Registry"
    echo "  REPLICATION_REGION: Azure region name of the replication to check/create"
    echo ""
    echo "This script will:"
    echo "  1. Delete any failed replications in the specified region"
    echo "  2. Create a new replication if none exists in that region"
    echo "  Note: Replica will be named after the region name"
    echo ""
    echo "Example:"
    echo "  export ACR_NAME=myacr"
    echo "  export REPLICATION_REGION=eastus2"
    echo "  $0"
    exit 1
}

# Check if required environment variables are set
if [ -z "${ACR_NAME:-}" ]; then
    echo "Error: ACR_NAME environment variable is not set"
    usage
fi

if [ -z "${REPLICATION_REGION:-}" ]; then
    echo "Error: REPLICATION_REGION environment variable is not set"
    usage
fi

# Check if DRY_RUN mode is enabled
if [ -n "${DRY_RUN:-}" ]; then
    echo "DRY_RUN mode enabled - will only show what would be deleted, not actually delete anything"
    DRY_RUN_MODE=true
else
    DRY_RUN_MODE=false
fi

# Function to execute or just log a command based on DRY_RUN mode
execute() {
    if [ "$DRY_RUN_MODE" = true ]; then
        echo "[DRY_RUN] Command: $*"
    else
        "$@"
    fi
}

# Function to create a new replication
create_replication() {
    echo "Creating replication $REPLICATION_REGION for ACR $ACR_NAME in region $REPLICATION_REGION..."
    execute az acr replication create \
        --registry "$ACR_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --location "$REPLICATION_REGION" \
        --name "$REPLICATION_REGION" \
        --region-endpoint-enabled true

    echo "Successfully created replication $REPLICATION_REGION for ACR $ACR_NAME in region $REPLICATION_REGION"
}

echo "Managing ACR replication for $ACR_NAME in region $REPLICATION_REGION..."

# Get the resource group and location for the ACR
echo "Getting ACR information for $ACR_NAME..."
ACR_INFO=$(az acr show --name "$ACR_NAME" --query '{resourceGroup: resourceGroup, location: location}' -o json)
RESOURCE_GROUP=$(echo "$ACR_INFO" | jq -r '.resourceGroup')
ACR_HOME_REGION=$(echo "$ACR_INFO" | jq -r '.location')
echo "ACR $ACR_NAME is in resource group: $RESOURCE_GROUP, home region: $ACR_HOME_REGION"

# Check if target region is the same as ACR home region
if [ "$REPLICATION_REGION" = "$ACR_HOME_REGION" ]; then
    echo "The ACR is homed in the region $REPLICATION_REGION - replication is only needed for different regions"
    exit 0
fi

# Check if any replication exists in the region
echo "Checking for existing replications in region $REPLICATION_REGION..."
# we need to query the existance of a replica via az resource list instead az acr replication list
# because the list operation is bugged and reports the wrong replication state at times
REPLICATION_INFO=$(az resource list \
    --resource-group "$RESOURCE_GROUP" \
    --resource-type "Microsoft.ContainerRegistry/registries/replications" \
    --query "[?location=='$REPLICATION_REGION' && contains(id, '/registries/$ACR_NAME/')] | [0]" \
    --output json
)

if [ -n "$REPLICATION_INFO" ]; then
    REPLICATION_RESOURCE_ID=$(echo "$REPLICATION_INFO" | jq -r '.id')
    REPLICATION_NAME=$(echo "$REPLICATION_INFO" | jq -r '.name' | cut -f 2 -d "/")
    # we need to query the replication state from the replica resource id and not from the list operation or the ACR
    # there are bugs flying around that report the wrong replication state on the list operation
    REPLICATION_STATE=$(az resource show --ids "$REPLICATION_RESOURCE_ID" --query "properties.provisioningState" -o tsv)
    echo "Found existing replication $REPLICATION_NAME ($REPLICATION_RESOURCE_ID) in state $REPLICATION_STATE"

    # Only check for failed replications if one exists
    if [ "$REPLICATION_STATE" = "Failed" ]; then
        echo "Replication $REPLICATION_RESOURCE_ID is in failed state. Deleting it..."
        execute az acr replication delete \
            --registry "$ACR_NAME" \
            --resource-group "$RESOURCE_GROUP" \
            --name "$REPLICATION_NAME"
        echo "Successfully deleted failed replication $REPLICATION_NAME"

        # After deleting failed replication, create a new one
        create_replication
    else
        echo "Replication already exists and is in good state: $REPLICATION_NAME (state: $REPLICATION_STATE)"
        exit 0
    fi
else
    echo "No replication exists in region $REPLICATION_REGION. Creating new replication..."
    create_replication
fi
