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
    echo "  DRY_RUN: true/1/yes to simulate deletions; false/0/no (default) to delete"
    echo ""
    echo "Examples:"
    echo "  # Normal cleanup"
    echo "  export REGIONAL_RESOURCE_GROUP=my-cleanup-rg"
    echo "  $0"
    echo ""
    echo "  # Dry run to see what would be deleted"
    echo "  export REGIONAL_RESOURCE_GROUP=my-cleanup-rg"
    echo "  export DRY_RUN=true"
    echo "  $0"
    exit 1
}

# Check if required environment variables are set
if [ -z "${REGIONAL_RESOURCE_GROUP:-}" ]; then
    echo "Error: REGIONAL_RESOURCE_GROUP environment variable is not set"
    usage
fi



# Accept the common truthy/falsy spellings and reject anything else. A bare
# equality test against "true" silently deleted for real on DRY_RUN=1, while
# treating every non-empty value as truthy would silently skip a real cleanup on
# DRY_RUN=false. Both failure modes are silent, so unrecognized values are a
# hard error instead of a guess.
case "${DRY_RUN:-false}" in
    true|TRUE|True|1|yes|YES|Yes)
        echo "DRY_RUN mode enabled - will only show what would be deleted, not actually delete anything"
        DRY_RUN_MODE=true
        ;;
    false|FALSE|False|0|no|NO|No|"")
        DRY_RUN_MODE=false
        ;;
    *)
        echo "Error: DRY_RUN must be one of true/1/yes or false/0/no, got '${DRY_RUN}'" >&2
        exit 1
        ;;
esac

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
if ! workspaces=$(az monitor account list --resource-group "$REGIONAL_RESOURCE_GROUP" --query "[].name" -o tsv); then
    echo "Error: failed to list Azure monitoring workspaces in resource group '$REGIONAL_RESOURCE_GROUP'" >&2
    exit 1
fi

if [ -z "$workspaces" ]; then
    echo "No monitoring workspaces found in resource group $REGIONAL_RESOURCE_GROUP"
    exit 0
fi

echo "Found workspaces: $workspaces"

failed=0

# Check each workspace for the aroHCPPurpose tag
while IFS= read -r workspace; do
    [ -n "$workspace" ] || continue

    echo "Checking workspace: $workspace"

    # Get the tags for this workspace. A lookup failure must never be treated as
    # "untagged", because that would delete a workspace we were meant to preserve.
    if ! aro_purpose_tag=$(az monitor account show --name "$workspace" --resource-group "$REGIONAL_RESOURCE_GROUP" --query "tags.aroHCPPurpose" -o tsv); then
        echo "Error: failed to read tags for workspace '$workspace' - preserving it and continuing" >&2
        failed=1
        continue
    fi

    if [ "$aro_purpose_tag" = "null" ] || [ -z "$aro_purpose_tag" ]; then
        echo "Workspace '$workspace' does not have aroHCPPurpose tag - deleting"
        if ! execute az monitor account delete --name "$workspace" --resource-group "$REGIONAL_RESOURCE_GROUP" --yes; then
            echo "Error: failed to delete workspace '$workspace'" >&2
            failed=1
        fi
    else
        echo "Workspace '$workspace' has aroHCPPurpose tag: '$aro_purpose_tag' - preserving"
    fi
done <<< "$workspaces"

if [ "$failed" -ne 0 ]; then
    echo "Error: one or more monitoring workspaces could not be processed" >&2
    exit 1
fi
