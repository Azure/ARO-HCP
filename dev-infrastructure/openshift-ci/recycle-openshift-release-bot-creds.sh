#!/bin/bash
set -euo pipefail

# Script to recycle (rotate) credentials for the Test Test Azure Red Hat OpenShift tenant
# Updates aro-hcp-stg-test-tenant and aro-hcp-prod-test-tenant Vault secrets
#
# Usage:
#   ./recycle-openshift-release-bot-creds.sh              # Rotate and update both stg and prod
#   ./recycle-openshift-release-bot-creds.sh --env stg    # Rotate and update only STAGE
#   ./recycle-openshift-release-bot-creds.sh --env prod   # Rotate and update only PROD
#   ./recycle-openshift-release-bot-creds.sh --delete-old # Delete old credentials when rotating
#
# This script rotates the Azure AD app credentials and updates the Vault secrets.
# Use this when credentials are expiring or need to be rotated for security.

APPLICATION_NAME="OpenShift Release Bot MSFT Test"
TENANT_ID="93b21e64-4824-439a-b893-46c9b2a51082"
VAULT_URL="https://vault.ci.openshift.org"

DELETE_OLD=false
TARGET_ENV=""

# Subscription info
STG_SUBSCRIPTION_ID="99399281-00a2-4b39-bb3d-b2645bbbdb93"
STG_SUBSCRIPTION_NAME="ARO HCP E2E - Staging"
PROD_SUBSCRIPTION_ID="403d9de9-132b-4974-94a5-5b78bdfa191e"
PROD_SUBSCRIPTION_NAME="ARO HCP E2E"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --delete-old)
            DELETE_OLD=true
            shift
            ;;
        --env)
            TARGET_ENV="$2"
            if [[ "$TARGET_ENV" != "stg" && "$TARGET_ENV" != "prod" ]]; then
                echo "Error: --env must be 'stg' or 'prod'"
                exit 1
            fi
            shift 2
            ;;
        --help|-h)
            echo "Usage: $0 [--delete-old] [--env stg|prod]"
            echo ""
            echo "Options:"
            echo "  --delete-old  Delete old credentials (default: keep them)"
            echo "  --env ENV     Environment to rotate (stg or prod)"
            echo "                If not specified, updates both stg and prod"
            echo ""
            echo "This script rotates credentials for the Test Test Azure Red Hat OpenShift tenant."
            echo "It updates aro-hcp-{env}-test-tenant Vault secrets."
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

update_vault_secret() {
    local env="$1"
    local client_id="$2"
    local client_secret="$3"
    
    local subscription_id subscription_name
    if [[ "${env}" == "stg" ]]; then
        subscription_id="${STG_SUBSCRIPTION_ID}"
        subscription_name="${STG_SUBSCRIPTION_NAME}"
    else
        subscription_id="${PROD_SUBSCRIPTION_ID}"
        subscription_name="${PROD_SUBSCRIPTION_NAME}"
    fi
    
    local vault_path="kv/selfservice/hcm-aro/aro-hcp-${env}-test-tenant"
    
    echo "Updating Vault secret: ${vault_path}"
    echo "  Subscription: ${subscription_name}"
    
    # Check if secret exists - use put for new, patch for existing
    if vault kv get "${vault_path}" > /dev/null 2>&1; then
        # Secret exists, use patch to preserve other fields
        vault kv patch "${vault_path}" \
            client-id="${client_id}" \
            client-secret="${client_secret}" \
            tenant="${TENANT_ID}" \
            subscription-id="${subscription_id}" \
            subscription-name="${subscription_name}"
    else
        # Secret doesn't exist, use put to create
        vault kv put "${vault_path}" \
            client-id="${client_id}" \
            client-secret="${client_secret}" \
            tenant="${TENANT_ID}" \
            subscription-id="${subscription_id}" \
            subscription-name="${subscription_name}"
    fi
    
    echo "  Updated ${vault_path}"
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

# Update vault secrets
if [[ -n "${TARGET_ENV}" ]]; then
    # Update only the specified environment
    update_vault_secret "${TARGET_ENV}" "${CLIENT_ID}" "${CLIENT_SECRET}"
else
    # Update both environments
    update_vault_secret "stg" "${CLIENT_ID}" "${CLIENT_SECRET}"
    update_vault_secret "prod" "${CLIENT_ID}" "${CLIENT_SECRET}"
fi

# Clear secret from memory
unset CLIENT_SECRET

header "Credential Rotation Complete"
echo ""
if [[ -n "${TARGET_ENV}" ]]; then
    echo "Updated secret: kv/selfservice/hcm-aro/aro-hcp-${TARGET_ENV}-test-tenant"
    echo ""
    echo "To apply to active secrets, run:"
    echo "  ./switch-vault-tenant.sh --to test-tenant --env ${TARGET_ENV}"
else
    echo "Updated secrets:"
    echo "  - kv/selfservice/hcm-aro/aro-hcp-stg-test-tenant"
    echo "  - kv/selfservice/hcm-aro/aro-hcp-prod-test-tenant"
    echo ""
    echo "To apply to active secrets, run:"
    echo "  ./switch-vault-tenant.sh --to test-tenant"
fi
