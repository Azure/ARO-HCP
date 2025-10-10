#!/bin/bash

DELETE_OLD_SECRET=false
VAULT_URL=""
VAULT_SECRET_PATH=""
TARGET_NAME=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --app)
            APPLICATION_NAME="$2"
            shift 2
            ;;
        --delete-old)
            DELETE_OLD_SECRET=true
            shift
            ;;
        --vault-url)
            VAULT_URL="$2"
            shift 2
            ;;
        --vault-secret)
            VAULT_SECRET_PATH="kv/$2"
            shift 2
            ;;
        --target-name)
            TARGET_NAME="$2"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1"
            exit 1
            ;;
    esac
done

if [[ -z "${APPLICATION_NAME:-}" ]]; then
    echo "Error: --app flag is required"
    exit 1
fi

if [[ -z "${VAULT_URL:-}" ]]; then
    echo "Error: --vault-url flag is required"
    exit 1
fi

if [[ -z "${VAULT_SECRET_PATH:-}" ]]; then
    echo "Error: --vault-secret flag is required"
    exit 1
fi

if [[ -z "${TARGET_NAME:-}" ]]; then
    echo "Error: --target-name flag is required"
    exit 1
fi

# Login to Vault
echo "Logging into Vault at ${VAULT_URL}"
export VAULT_ADDR="${VAULT_URL}"

# Check if vault CLI is available
if ! command -v vault &> /dev/null; then
    echo "Error: vault CLI is not available"
    exit 1
fi

# Authenticate with Vault using OIDC
echo "Authenticating with Vault using OIDC..."
vault login --method=oidc > /dev/null 2>&1
if [[ $? -ne 0 ]]; then
    echo "Error: Failed to authenticate with Vault using OIDC method."
    exit 1
fi

echo "Successfully authenticated with Vault"

# Check if secret already exists
echo "Checking if secret exists at path: ${VAULT_SECRET_PATH}"
if vault kv get "${VAULT_SECRET_PATH}" > /dev/null 2>&1; then
    echo "Secret already exists at ${VAULT_SECRET_PATH}"
else
    echo "Secret does not exist at ${VAULT_SECRET_PATH} - will create new one"
fi


echo "Recycling credentials for application ${APPLICATION_NAME} with appId ${APP_ID}"
APP_ID=$(az ad app list --display-name "${APPLICATION_NAME}" --query '[*]'.appId -o tsv)
if [[ -z "${APP_ID}" ]]; then
    echo "Application ${APPLICATION_NAME} not found"
    exit 1
fi

if [[ "${DELETE_OLD_SECRET}" == "true" ]]; then
    echo "Resetting credentials (this will delete all existing credentials)"
    CREDS_OUTPUT=$(az ad app credential reset \
        --id "${APP_ID}" \
        -o json
    )
else
    echo "Creating new credential (keeping existing ones)"
    CREDS_OUTPUT=$(az ad app credential reset \
        --id "${APP_ID}" \
        --append \
        -o json
    )
fi

echo "Credential operation completed"

# Extract credentials from the output
CLIENT_ID=$(echo "${CREDS_OUTPUT}" | jq -r '.appId')
CLIENT_SECRET=$(echo "${CREDS_OUTPUT}" | jq -r '.password')
TENANT_ID=$(echo "${CREDS_OUTPUT}" | jq -r '.tenant')

echo "Writing credentials to Vault at ${VAULT_SECRET_PATH}"
vault kv put "${VAULT_SECRET_PATH}" \
    client-id="${CLIENT_ID}" \
    client-secret="${CLIENT_SECRET}" \
    secretsync/target-name="${TARGET_NAME}" \
    secretsync/target-namespace="test-credentials" \
    tenant="${TENANT_ID}"

if [[ $? -eq 0 ]]; then
    echo "Successfully wrote credentials to Vault"
    echo "Credentials stored:"
    echo "  Client ID: ${CLIENT_ID}"
    echo "  Tenant ID: ${TENANT_ID}"
    echo "  Target Name: ${TARGET_NAME}"
    echo "  Target Namespace: test-credentials"
    echo "  Client Secret: [REDACTED]"
else
    echo "Error: Failed to write credentials to Vault"
    exit 1
fi
