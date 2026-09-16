#!/bin/bash
set -euo pipefail

: "${APP_OBJECT_ID:?APP_OBJECT_ID is required}"
: "${APP_ID:?APP_ID is required}"
: "${KEY_VAULT_NAME:?KEY_VAULT_NAME is required}"
: "${CLIENT_ID_NAME:?CLIENT_ID_NAME is required}"
: "${CLIENT_SECRET_NAME:?CLIENT_SECRET_NAME is required}"

umask 077
secret_dir=$(mktemp -d)
trap 'find "${secret_dir}" -type f -delete; rmdir "${secret_dir}"' EXIT

kv_secret_exists() {
    local name="$1"
    local output
    output=$(az keyvault secret show --vault-name "${KEY_VAULT_NAME}" --name "${name}" 2>&1) && return 0
    if grep -q "SecretNotFound" <<<"${output}"; then
        return 1
    fi
    echo "ERROR: unexpected failure checking Key Vault secret '${name}':"
    echo "${output}"
    exit 1
}

rotate_client_secret=true
printf '%s' "${APP_ID}" >"${secret_dir}/client-id"
reconcile_client_id=true
if kv_secret_exists "${CLIENT_ID_NAME}"; then
    az keyvault secret download \
        --vault-name "${KEY_VAULT_NAME}" \
        --name "${CLIENT_ID_NAME}" \
        --file "${secret_dir}/existing-client-id" \
        --encoding utf-8 \
        --output none
    if cmp -s "${secret_dir}/client-id" "${secret_dir}/existing-client-id"; then
        reconcile_client_id=false
    fi
fi

if ${reconcile_client_id}; then
    az keyvault secret set \
        --vault-name "${KEY_VAULT_NAME}" \
        --name "${CLIENT_ID_NAME}" \
        --file "${secret_dir}/client-id" \
        --encoding utf-8 \
        --output none
    echo "Reconciled ${CLIENT_ID_NAME}"
else
    echo "${CLIENT_ID_NAME} already contains the current application ID, skipping"
fi

if kv_secret_exists "${CLIENT_SECRET_NAME}"; then
    client_secret_metadata=$(az keyvault secret show \
        --vault-name "${KEY_VAULT_NAME}" \
        --name "${CLIENT_SECRET_NAME}" \
        --query '{expires: attributes.expires, applicationId: tags.entraApplicationId}' \
        --output json)
    stored_application_id=$(jq -r '.applicationId // ""' <<<"${client_secret_metadata}")
    if [[ "${stored_application_id}" == "${APP_ID}" ]]; then
        client_secret_expiry=$(jq -r '.expires // ""' <<<"${client_secret_metadata}")
        if expiry_epoch=$(date -u -d "${client_secret_expiry}" +%s 2>/dev/null); then
            rotation_epoch=$(date -u -d '+30 days' +%s)
            if ((expiry_epoch > rotation_epoch)); then
                rotate_client_secret=false
            fi
        fi
    fi
fi

if ${rotate_client_secret}; then
    # Entra client secrets have a maximum lifetime of two years.
    credential_end_time=$(date -u -d '+2 years' '+%Y-%m-%dT%H:%M:%SZ')
    result=$(az rest \
        --method POST \
        --uri "https://graph.microsoft.com/v1.0/applications/${APP_OBJECT_ID}/addPassword" \
        --headers "Content-Type=application/json" \
        --body "{
            \"passwordCredential\": {
                \"displayName\": \"cihealth-auth-managed\",
                \"endDateTime\": \"${credential_end_time}\"
            }
        }")
    jq -erj '.secretText' <<<"${result}" >"${secret_dir}/client-secret"
    new_key_id=$(jq -er '.keyId' <<<"${result}")
    az keyvault secret set \
        --vault-name "${KEY_VAULT_NAME}" \
        --name "${CLIENT_SECRET_NAME}" \
        --file "${secret_dir}/client-secret" \
        --encoding utf-8 \
        --expires "${credential_end_time}" \
        --tags entraApplicationId="${APP_ID}" entraCredentialKeyId="${new_key_id}" \
        --output none
    echo "Created a new version of ${CLIENT_SECRET_NAME}"
else
    echo "${CLIENT_SECRET_NAME} remains valid for more than 30 days, skipping rotation"
fi
