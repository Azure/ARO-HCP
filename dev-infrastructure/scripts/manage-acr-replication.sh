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

echo "Managing ACR replication for $ACR_NAME in region $REPLICATION_REGION..."

# Get the resource group and location for the ACR
echo "Getting ACR information for $ACR_NAME..."
ACR_INFO=$(az acr show --name "$ACR_NAME" --query '{resourceGroup: resourceGroup, location: location}' -o json)
RESOURCE_GROUP=$(echo "$ACR_INFO" | jq -r '.resourceGroup')
ACR_HOME_REGION=$(echo "$ACR_INFO" | jq -r '.location')
echo "ACR $ACR_NAME is in resource group: $RESOURCE_GROUP, home region: $ACR_HOME_REGION"

# Check if target region is the same as ACR home region
if [ "$REPLICATION_REGION" = "$ACR_HOME_REGION" ]; then
    echo "Error: Cannot create replication in the same region ($REPLICATION_REGION) as the ACR's home region"
    echo "The ACR already exists in region $ACR_HOME_REGION - replication is only needed for different regions"
    exit 0
fi

# Step 1: Check for and delete any failed replications
echo "Searching for failed replications in region $REPLICATION_REGION..."
FAILED_REPLICATION=$(az acr replication list \
    --registry "$ACR_NAME" \
    --query "[?location=='$REPLICATION_REGION' && provisioningState=='Failed'] | [0]" \
    --output json)

if [ -n "$FAILED_REPLICATION" ]; then
    # Extract the replication name and delete it
    FAILED_REPLICATION_NAME=$(echo "$FAILED_REPLICATION" | jq -r '.name')
    echo "Found failed replication: $FAILED_REPLICATION_NAME"

    echo "Deleting failed replication $FAILED_REPLICATION_NAME for ACR $ACR_NAME in region $REPLICATION_REGION..."
    az acr replication delete \
        --registry "$ACR_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --name "$FAILED_REPLICATION_NAME"

    echo "Successfully deleted failed replication $FAILED_REPLICATION_NAME"
else
    echo "No failed replications found in region $REPLICATION_REGION"
fi

# Step 2: Check if any replication exists in the region
echo "Checking for existing replications in region $REPLICATION_REGION..."
EXISTING_REPLICATION=$(az acr replication list \
    --registry "$ACR_NAME" \
    --query "[?location=='$REPLICATION_REGION'] | [0]" \
    --output json)

if [ -z "$EXISTING_REPLICATION" ]; then
    echo "No replication exists in region $REPLICATION_REGION. Creating new replication..."

    # Create new replication
    echo "Creating replication $REPLICATION_REGION for ACR $ACR_NAME in region $REPLICATION_REGION..."
    az acr replication create \
        --registry "$ACR_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --location "$REPLICATION_REGION" \
        --name "$REPLICATION_REGION" \
        --region-endpoint-enabled true

    echo "Successfully created replication $REPLICATION_REGION for ACR $ACR_NAME in region $REPLICATION_REGION"
else
    EXISTING_NAME=$(echo "$EXISTING_REPLICATION" | jq -r '.name')
    EXISTING_STATE=$(echo "$EXISTING_REPLICATION" | jq -r '.provisioningState')
    echo "Replication already exists: $EXISTING_NAME (state: $EXISTING_STATE)"
fi 
