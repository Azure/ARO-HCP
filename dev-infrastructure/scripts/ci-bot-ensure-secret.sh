#!/bin/bash
set -euo pipefail

# Ensure a CI bot application has its credentials stored in Key Vault.
# Backfills any missing secrets without regenerating existing ones.
#
# Required environment variables:
#   APP_NAME       - Entra application display name (uniqueName)
#   ENV_NAME       - Environment label (e.g. "stg") used in KV secret names
#   KEY_VAULT_NAME - Target Azure Key Vault name

: "${APP_NAME:?APP_NAME is required}"
: "${ENV_NAME:?ENV_NAME is required}"
: "${KEY_VAULT_NAME:?KEY_VAULT_NAME is required}"

SECRET_PREFIX="ci-bot-${ENV_NAME}"

# kv_secret_exists: returns 0 if the secret exists, 1 if SecretNotFound,
# and exits on any other error.
kv_secret_exists() {
    local name="$1"
    local output
    output=$(az keyvault secret show --vault-name "${KEY_VAULT_NAME}" --name "${name}" 2>&1) && return 0
    if echo "${output}" | grep -q "SecretNotFound"; then
        return 1
    fi
    echo "ERROR: unexpected failure checking Key Vault secret '${name}':"
    echo "${output}"
    exit 1
}

echo "Checking existing secrets in ${KEY_VAULT_NAME}..."
HAVE_SECRET=false; HAVE_CLIENT_ID=false; HAVE_TENANT_ID=false
kv_secret_exists "${SECRET_PREFIX}-client-secret" && HAVE_SECRET=true
kv_secret_exists "${SECRET_PREFIX}-client-id" && HAVE_CLIENT_ID=true
kv_secret_exists "${SECRET_PREFIX}-tenant-id" && HAVE_TENANT_ID=true

if $HAVE_SECRET && $HAVE_CLIENT_ID && $HAVE_TENANT_ID; then
    echo "All secrets already present, nothing to do"
    exit 0
fi

echo "Looking up application '${APP_NAME}'..."
APP_OBJECT_ID=$(az ad app list --display-name "${APP_NAME}" --query "[?displayName=='${APP_NAME}'].id" -o tsv)
APP_CLIENT_ID=$(az ad app list --display-name "${APP_NAME}" --query "[?displayName=='${APP_NAME}'].appId" -o tsv)

if [[ -z "${APP_OBJECT_ID}" ]]; then
    echo "ERROR: application '${APP_NAME}' not found"
    exit 1
fi

# Admin consent for declared API permissions (e.g. Application.ReadWrite.OwnedBy).
# This is best-effort: if the pipeline executor lacks the required Entra role
# (e.g. Cloud Application Administrator or Global Administrator), the call will
# fail and a tenant admin must grant consent manually before the bot is fully
# usable. The warning below is NOT self-healing — treat it as a manual action item.
echo "Granting admin consent for declared API permissions..."
az ad app permission admin-consent --id "${APP_CLIENT_ID}" || {
    echo "WARNING: admin-consent failed — a tenant admin must grant consent manually"
    echo "         before the bot can exercise Application.ReadWrite.OwnedBy."
    echo "         This will NOT resolve itself on subsequent pipeline runs."
}

TENANT_ID=$(az account show --query tenantId -o tsv)

if ! $HAVE_SECRET; then
    echo "Generating client secret for application ${APP_CLIENT_ID} (${APP_NAME})..."
    RESULT=$(az rest \
        --method POST \
        --uri "https://graph.microsoft.com/v1.0/applications/${APP_OBJECT_ID}/addPassword" \
        --headers "Content-Type=application/json" \
        --body "{
            \"passwordCredential\": {
                \"displayName\": \"${SECRET_PREFIX}-managed\",
                \"endDateTime\": \"$(date -u -d '+2 years' '+%Y-%m-%dT%H:%M:%SZ')\"
            }
        }")
    CLIENT_SECRET=$(echo "${RESULT}" | jq -r '.secretText')
    echo "Storing ${SECRET_PREFIX}-client-secret..."
    az keyvault secret set --vault-name "${KEY_VAULT_NAME}" --name "${SECRET_PREFIX}-client-secret" --value "${CLIENT_SECRET}" --output none
else
    echo "${SECRET_PREFIX}-client-secret already exists, skipping generation"
fi

if ! $HAVE_CLIENT_ID; then
    echo "Storing ${SECRET_PREFIX}-client-id..."
    az keyvault secret set --vault-name "${KEY_VAULT_NAME}" --name "${SECRET_PREFIX}-client-id" --value "${APP_CLIENT_ID}" --output none
else
    echo "${SECRET_PREFIX}-client-id already exists, skipping"
fi

if ! $HAVE_TENANT_ID; then
    echo "Storing ${SECRET_PREFIX}-tenant-id..."
    az keyvault secret set --vault-name "${KEY_VAULT_NAME}" --name "${SECRET_PREFIX}-tenant-id" --value "${TENANT_ID}" --output none
else
    echo "${SECRET_PREFIX}-tenant-id already exists, skipping"
fi

echo "Done. All secrets present as ${SECRET_PREFIX}-{client-secret,client-id,tenant-id} in ${KEY_VAULT_NAME}"
