#!/bin/bash
set -euo pipefail

# Rotate the OpenShift Release Bot credentials used by the active
# aro-hcp-prod cluster profile.
#
# Usage:
#   ./recycle-openshift-release-bot-creds.sh
#   ./recycle-openshift-release-bot-creds.sh --delete-old # Delete old credentials when rotating
#
# The script patches only identity fields. It intentionally preserves the
# customer-shard subscription inventory and all secretsync metadata.

APPLICATION_NAME="OpenShift Release Bot MSFT Test"
TENANT_ID="93b21e64-4824-439a-b893-46c9b2a51082"
VAULT_URL="https://vault.ci.openshift.org"

DELETE_OLD=false
VAULT_PATH="kv/selfservice/hcm-aro/aro-hcp-prod"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --delete-old)
            DELETE_OLD=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [--delete-old]"
            echo ""
            echo "Options:"
            echo "  --delete-old  Delete old credentials (default: keep them)"
            echo ""
            echo "This script rotates credentials for the Test Test Azure Red Hat OpenShift tenant"
            echo "and patches the active aro-hcp-prod Vault secret."
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

header() {
    echo ""
    echo "------"
    echo "$1"
    echo "------"
    echo ""
}

# Check prerequisites
if ! command -v vault &> /dev/null; then
    echo "Error: vault CLI is not installed"
    exit 1
fi

if ! command -v az &> /dev/null; then
    echo "Error: az CLI is not installed"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "Error: jq is not installed"
    exit 1
fi

# Get the app ID
header "Looking up application: ${APPLICATION_NAME}"
APP_ID=$(az ad app list --display-name "${APPLICATION_NAME}" --query '[*].appId' -o tsv)

if [[ -z "${APP_ID}" ]]; then
    echo "Error: Application '${APPLICATION_NAME}' not found"
    echo "Run create-openshift-release-bot-msft-test.sh first"
    exit 1
fi

echo "  App ID: ${APP_ID}"

# Rotate credentials
header "Rotating Credentials"

if [[ "${DELETE_OLD}" == "true" ]]; then
    echo "Resetting credentials (deleting all existing)..."
    CRED_OUTPUT=$(az ad app credential reset \
        --id "${APP_ID}" \
        -o json)
else
    echo "Creating new credentials (keeping existing)..."
    CRED_OUTPUT=$(az ad app credential reset \
        --id "${APP_ID}" \
        --append \
        --display-name "OpenShift CI $(date +%Y-%m-%d)" \
        -o json)
fi

CLIENT_ID=$(echo "${CRED_OUTPUT}" | jq -r '.appId')
CLIENT_SECRET=$(echo "${CRED_OUTPUT}" | jq -r '.password')

echo "  New credentials generated"

# Login to Vault (skip if already logged in)
header "Updating Vault Secrets"

export VAULT_ADDR="${VAULT_URL}"
if vault token lookup > /dev/null 2>&1; then
    echo "Already logged into Vault"
else
    echo "Logging into Vault (browser will open)..."
    vault login --method=oidc > /dev/null 2>&1
    echo "Successfully logged into Vault"
fi

echo "Patching Vault secret: ${VAULT_PATH}"
vault kv patch "${VAULT_PATH}" \
    client-id="${CLIENT_ID}" \
    client-secret="${CLIENT_SECRET}" \
    tenant="${TENANT_ID}"
echo "  Updated identity fields in ${VAULT_PATH}"

# Clear secret from memory
unset CLIENT_SECRET

header "Credential Rotation Complete"
echo ""
echo "Updated secret: ${VAULT_PATH}"
echo "Customer subscription fields and secretsync metadata were preserved."
