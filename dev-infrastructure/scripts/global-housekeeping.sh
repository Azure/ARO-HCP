#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

# Function to display usage
usage() {
    echo "Usage: Set environment variables and run the script"
    echo ""
    echo "Required environment variables:"
    echo "  GLOBAL_RESOURCE_GROUP: Azure resource group containing resources to clean up"
    echo ""
    echo "Optional environment variables:"
    echo "  DRY_RUN: Set to any value to simulate deletions without actually deleting"
    echo ""
    echo "Examples:"
    echo "  # Normal cleanup"
    echo "  export GLOBAL_RESOURCE_GROUP=my-cleanup-rg"
    echo "  $0"
    echo ""
    echo "  # Dry run to see what would be deleted"
    echo "  export GLOBAL_RESOURCE_GROUP=my-cleanup-rg"
    echo "  export DRY_RUN=1"
    echo "  $0"
    exit 1
}

# Check if required environment variables are set
if [ -z "${GLOBAL_RESOURCE_GROUP:-}" ]; then
    echo "Error: GLOBAL_RESOURCE_GROUP environment variable is not set"
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

#
#   C O N T A I N E R   A P P   J O B S
#

CONTAINER_APP_JOBS_TO_DELETE=("acm-mirror" "component-sync" "oc-mirror-4-18" "oc-mirror-4-19" "oc-mirror")
for job in "${CONTAINER_APP_JOBS_TO_DELETE[@]}"; do
    if az containerapp job show --name "$job" --resource-group "$GLOBAL_RESOURCE_GROUP" &>/dev/null; then
        execute az containerapp job delete --name "$job" --resource-group "$GLOBAL_RESOURCE_GROUP" --yes
    else
        echo "Container app job '$job' does not exist in resource group '$GLOBAL_RESOURCE_GROUP', skipping."
    fi
done
