#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

# Function to display usage
usage() {
    echo "Usage: Set environment variables and run the script"
    echo ""
    echo "Required environment variables:"
    echo "  REGIONAL_RESOURCE_GROUP: Azure resource group containing regional resources to clean up"
    echo ""
    echo "Optional environment variables:"
    echo "  DRY_RUN: Set to any value to simulate deletions without actually deleting"
    echo ""
    exit 1
}

# Check if required environment variables are set
if [ -z "${REGIONAL_RESOURCE_GROUP:-}" ]; then
    echo "Error: REGIONAL_RESOURCE_GROUP environment variable is not set"
    usage
fi



# Check if DRY_RUN mode is enabled
if [ "${DRY_RUN:-false}" == "true" ]; then
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

#
#   A Z U R E   M O N I T O R I N G   W O R K S P A C E S
#

echo "Discovering Azure monitoring workspaces in resource group $REGIONAL_RESOURCE_GROUP..."

# Get all monitoring workspaces in the resource group
workspaces=$(az monitor account list --resource-group "$REGIONAL_RESOURCE_GROUP" --query "[].name" -o tsv)

if [ -z "$workspaces" ]; then
    echo "No monitoring workspaces found in resource group $REGIONAL_RESOURCE_GROUP"
    exit 0
fi

echo "Found workspaces: $workspaces"

# Check each workspace for the aroHCPPurpose tag
for workspace in $workspaces; do
    echo "Checking workspace: $workspace"

    # Get the tags for this workspace
    aro_purpose_tag=$(az monitor account show --name "$workspace" --resource-group "$REGIONAL_RESOURCE_GROUP" --query "tags.aroHCPPurpose" -o tsv 2>/dev/null)

    if [ "$aro_purpose_tag" = "null" ] || [ -z "$aro_purpose_tag" ]; then
        echo "Workspace '$workspace' does not have aroHCPPurpose tag - deleting"
        execute az monitor account delete --name "$workspace" --resource-group "$REGIONAL_RESOURCE_GROUP" --yes
    else
        echo "Workspace '$workspace' has aroHCPPurpose tag: '$aro_purpose_tag' - preserving"
    fi
done
