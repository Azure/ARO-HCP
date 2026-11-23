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
    echo "  GLOBAL_KEY_VAULT: global Key Vault to delete retired secrets from"
    echo ""
    echo "Optional environment variables:"
    echo "  RETIRED_KEY_VAULT_SECRETS: Comma-separated names of secrets to delete from GLOBAL_KEY_VAULT"
    echo "  DRY_RUN: Set to any value to simulate deletions without actually deleting"
    echo ""
    echo "Examples:"
    echo "  # Normal cleanup"
    echo "  export GLOBAL_RESOURCE_GROUP=my-cleanup-rg"
    echo "  export GLOBAL_KEY_VAULT=my-global-kv"
    echo "  $0"
    echo ""
    echo "  # Dry run to see what would be deleted"
    echo "  export GLOBAL_RESOURCE_GROUP=my-cleanup-rg"
    echo "  export GLOBAL_KEY_VAULT=my-global-kv"
    echo "  export DRY_RUN=1"
    echo "  $0"
    exit 1
}

# Check if required environment variables are set
if [ -z "${GLOBAL_RESOURCE_GROUP:-}" ]; then
    echo "Error: GLOBAL_RESOURCE_GROUP environment variable is not set"
    usage
fi
if [ -z "${GLOBAL_KEY_VAULT:-}" ]; then
    echo "Error: GLOBAL_KEY_VAULT environment variable is not set"
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
#   this is the place where you want to add housekeeping logic for global resources
#   keep in mind that we don't need this housekeeping anymore once azuer deployment
#   stacks become available in EV2
#

# The SecretSync step only writes the secrets declared in data/encryptedsecrets.yaml, so a secret
# removed from that file keeps its copy in the vault until it is deleted here (soft delete).
if [ -n "${RETIRED_KEY_VAULT_SECRETS:-}" ]; then
    existing_secrets="$(az keyvault secret list --vault-name "${GLOBAL_KEY_VAULT}" --query "[].name" --output tsv)"
    for secret in ${RETIRED_KEY_VAULT_SECRETS//,/ }; do
        if ! grep -qxF "${secret}" <<< "${existing_secrets}"; then
            echo "Retired secret ${secret} is already absent from Key Vault ${GLOBAL_KEY_VAULT}"
            continue
        fi
        echo "Deleting retired secret ${secret} from Key Vault ${GLOBAL_KEY_VAULT}"
        execute az keyvault secret delete --vault-name "${GLOBAL_KEY_VAULT}" --name "${secret}" --output none
    done
fi
