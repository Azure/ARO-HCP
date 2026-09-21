#!/bin/bash
set -euo pipefail

: "${SOURCE_KEY_VAULT_NAME:?SOURCE_KEY_VAULT_NAME is required}"
: "${TARGET_KEY_VAULT_NAME:?TARGET_KEY_VAULT_NAME is required}"
: "${CLIENT_ID_NAME:?CLIENT_ID_NAME is required}"
: "${CLIENT_SECRET_NAME:?CLIENT_SECRET_NAME is required}"
: "${COOKIE_SECRET_NAME:?COOKIE_SECRET_NAME is required}"
: "${POSTGRES_USERNAME_SECRET_NAME:?POSTGRES_USERNAME_SECRET_NAME is required}"
: "${POSTGRES_PASSWORD_SECRET_NAME:?POSTGRES_PASSWORD_SECRET_NAME is required}"
: "${POSTGRES_DATABASE_SECRET_NAME:?POSTGRES_DATABASE_SECRET_NAME is required}"

umask 077
secret_dir=$(mktemp -d)
trap 'find "${secret_dir}" -type f -delete; rmdir "${secret_dir}"' EXIT

target_secret_exists() {
    local name="$1"
    local output
    output=$(az keyvault secret show --vault-name "${TARGET_KEY_VAULT_NAME}" --name "${name}" 2>&1) && return 0
    if grep -q "SecretNotFound" <<<"${output}"; then
        return 1
    fi
    echo "ERROR: unexpected failure checking Key Vault secret '${name}':"
    echo "${output}"
    exit 1
}

set_target_secret_if_missing() {
    local name="$1"
    local file="$2"
    if target_secret_exists "${name}"; then
        echo "${name} already exists, skipping"
        return
    fi
    az keyvault secret set \
        --vault-name "${TARGET_KEY_VAULT_NAME}" \
        --name "${name}" \
        --file "${file}" \
        --encoding utf-8 \
        --output none
    echo "Created ${name}"
}

ensure_cookie_secret() {
    local file="${secret_dir}/cookie-secret"
    local size

    if target_secret_exists "${COOKIE_SECRET_NAME}"; then
        az keyvault secret download \
            --vault-name "${TARGET_KEY_VAULT_NAME}" \
            --name "${COOKIE_SECRET_NAME}" \
            --file "${file}" \
            --encoding utf-8 \
            --output none
        size=$(wc -c <"${file}")
        if [[ "${size}" == "16" || "${size}" == "24" || "${size}" == "32" ]]; then
            echo "${COOKIE_SECRET_NAME} has a valid AES key length, skipping"
            return
        fi
        echo "${COOKIE_SECRET_NAME} has invalid length ${size}, replacing"
    fi

    openssl rand -hex 16 | tr -d '\n' >"${file}"
    az keyvault secret set \
        --vault-name "${TARGET_KEY_VAULT_NAME}" \
        --name "${COOKIE_SECRET_NAME}" \
        --file "${file}" \
        --encoding utf-8 \
        --output none
    echo "Reconciled ${COOKIE_SECRET_NAME}"
}

mirror_secret() {
    local name="$1"
    local file="${secret_dir}/${name}"
    local metadata
    local source_version
    local target_source_version
    local expires
    local normalized_expires
    local -a set_args

    metadata=$(az keyvault secret show \
        --vault-name "${SOURCE_KEY_VAULT_NAME}" \
        --name "${name}" \
        --query '{id: id, expires: attributes.expires}' \
        --output json)
    source_version=$(jq -er '.id | split("/") | last' <<<"${metadata}")

    if target_secret_exists "${name}"; then
        target_source_version=$(az keyvault secret show \
            --vault-name "${TARGET_KEY_VAULT_NAME}" \
            --name "${name}" \
            --query 'tags.sourceSecretVersion' \
            --output tsv)
        if [[ "${target_source_version}" == "${source_version}" ]]; then
            echo "${name} already mirrors source version ${source_version}, skipping"
            return
        fi
    fi

    az keyvault secret download \
        --vault-name "${SOURCE_KEY_VAULT_NAME}" \
        --name "${name}" \
        --file "${file}" \
        --encoding utf-8 \
        --output none

    set_args=(
        --vault-name "${TARGET_KEY_VAULT_NAME}"
        --name "${name}"
        --file "${file}"
        --encoding utf-8
        --tags sourceSecretVersion="${source_version}"
        --output none
    )
    expires=$(jq -r '.expires // ""' <<<"${metadata}")
    if [[ -n "${expires}" ]]; then
        if ! normalized_expires=$(date -u -d "${expires}" '+%Y-%m-%dT%H:%M:%SZ'); then
            echo "ERROR: invalid expiry on source Key Vault secret '${name}': ${expires}"
            exit 1
        fi
        set_args+=(--expires "${normalized_expires}")
    fi

    az keyvault secret set "${set_args[@]}"
    echo "Mirrored ${name} source version ${source_version}"
}

mirror_secret "${CLIENT_ID_NAME}"
mirror_secret "${CLIENT_SECRET_NAME}"

printf '%s' "cihealth_auth" >"${secret_dir}/postgres-username"
openssl rand -base64 32 >"${secret_dir}/postgres-password"
printf '%s' "cihealth_auth" >"${secret_dir}/postgres-database"

ensure_cookie_secret
set_target_secret_if_missing "${POSTGRES_USERNAME_SECRET_NAME}" "${secret_dir}/postgres-username"
set_target_secret_if_missing "${POSTGRES_PASSWORD_SECRET_NAME}" "${secret_dir}/postgres-password"
set_target_secret_if_missing "${POSTGRES_DATABASE_SECRET_NAME}" "${secret_dir}/postgres-database"
