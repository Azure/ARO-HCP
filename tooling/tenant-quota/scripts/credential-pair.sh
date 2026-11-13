#!/bin/bash

set +x
umask 077
PAIR_TEMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$PAIR_TEMP_DIR"' EXIT

pair_error() { echo "ERROR: $*" >&2; return 1; }

validate_pair_names() {
    [[ "$PAIR_SECRET_NAME" =~ ^[A-Za-z0-9-]{1,127}$ && "$PAIR_ID_NAME" =~ ^[A-Za-z0-9-]{1,127}$ ]] || return 1
    [[ "$PAIR_SECRET_NAME" == *-client-secret && "$PAIR_ID_NAME" == "${PAIR_SECRET_NAME%-client-secret}-client-id" ]] || return 1
}

read_vault_value() {
    local name="$1" error_file
    error_file=$(mktemp "$PAIR_TEMP_DIR/error.XXXXXX")
    if PAIR_VALUE=$(az keyvault secret show --vault-name "$KEYVAULT_NAME" --name "$name" --query value -o tsv 2>"$error_file"); then
        rm -f "$error_file"
        [[ -n "${PAIR_VALUE//[[:space:]]/}" ]] || { pair_error "Empty stored credential"; return 1; }
        PAIR_FOUND=true
    elif grep -q 'SecretNotFound' "$error_file"; then
        rm -f "$error_file"
        PAIR_VALUE=""
        PAIR_FOUND=false
    else
        rm -f "$error_file"
        pair_error "Key Vault lookup failed (not a missing secret)"
        return 1
    fi
}

validate_pair_values() {
    [[ "$PAIR_TENANT_ID" =~ ^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$ && "$PAIR_CLIENT_ID" =~ ^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$ ]] || { pair_error "Invalid tenant or client ID"; return 1; }
    [[ -n "${PAIR_PASSWORD//[[:space:]]/}" ]] || { pair_error "Empty password"; return 1; }
}

verify_pair() {
    validate_pair_values || return 1
    local form_file result
    form_file=$(mktemp "$PAIR_TEMP_DIR/form.XXXXXX") || { pair_error "Cannot create credential verification staging file"; return 1; }
    if ! printf '%s' "$PAIR_PASSWORD" | jq -Rs --arg client "$PAIR_CLIENT_ID" '"grant_type=client_credentials&scope=https%3A%2F%2Fmanagement.azure.com%2F.default&client_id=" + $client + "&client_secret=" + (. | @uri)' -j > "$form_file"; then
        rm -f -- "$form_file"
        pair_error "Cannot stage credential verification request; nothing published"
        return 1
    fi
    if ! result=$(curl --silent --show-error --fail --max-time 60 --header 'Content-Type: application/x-www-form-urlencoded' --data-binary "@$form_file" "https://login.microsoftonline.com/$PAIR_TENANT_ID/oauth2/v2.0/token" 2>/dev/null); then
        rm -f "$form_file"
        pair_error "Credential binding verification failed; nothing published"
        return 1
    fi
    rm -f "$form_file"
    printf '%s' "$result" | jq -e '.access_token | type == "string" and length > 0' >/dev/null 2>&1 || { pair_error "Token verification failed"; return 1; }
}

write_vault_value() {
    local name="$1" file
    file=$(mktemp "$PAIR_TEMP_DIR/value.XXXXXX") || { pair_error "Cannot create credential publication staging file"; return 1; }
    if ! cat > "$file"; then
        rm -f -- "$file"
        pair_error "Cannot stage credential publication; nothing written; retry with the same pair"
        return 1
    fi
    if ! az keyvault secret set --vault-name "$KEYVAULT_NAME" --name "$name" --file "$file" --output none 2>/dev/null; then
        rm -f "$file"
        pair_error "Pair publication incomplete; do not activate or restart; retry with the same pair"
        return 1
    fi
    rm -f "$file"
}

publish_pair() {
    local id_found secret_found existing_secret replacement
    validate_pair_names || { pair_error "Invalid paired secret references"; return 1; }
    read_vault_value "$PAIR_ID_NAME" || return 1
    if [[ "$PAIR_FOUND" == true && "$PAIR_VALUE" != "$PAIR_CLIENT_ID" ]]; then
        pair_error "Identity changed: stage new generation-specific names instead of overwriting the active pair"
        return 1
    fi
    id_found="$PAIR_FOUND"
    read_vault_value "$PAIR_SECRET_NAME" || return 1
    secret_found="$PAIR_FOUND"
    existing_secret="$PAIR_VALUE"
    if [[ "$id_found" == false && "$secret_found" == true && "$existing_secret" != "$PAIR_PASSWORD" ]]; then
        replacement="$PAIR_PASSWORD"
        PAIR_PASSWORD="$existing_secret"
        verify_pair || return 1
        PAIR_PASSWORD="$replacement"
    fi
    verify_pair || return 1
    if [[ "$secret_found" == false || "$existing_secret" != "$PAIR_PASSWORD" ]]; then
        printf '%s' "$PAIR_PASSWORD" | write_vault_value "$PAIR_SECRET_NAME" || return 1
    fi
    if [[ "$id_found" == false ]]; then
        printf '%s' "$PAIR_CLIENT_ID" | write_vault_value "$PAIR_ID_NAME" || return 1
    fi
    read_vault_value "$PAIR_SECRET_NAME" || return 1
    [[ "$PAIR_FOUND" == true && "$PAIR_VALUE" == "$PAIR_PASSWORD" ]] || { pair_error "Password read-back failed; do not activate"; return 1; }
    read_vault_value "$PAIR_ID_NAME" || return 1
    [[ "$PAIR_FOUND" == true && "$PAIR_VALUE" == "$PAIR_CLIENT_ID" ]] || { pair_error "Client ID read-back failed; do not activate"; return 1; }
    echo "Verified pair published. Restart the exporter and verify fresh collection before retiring old credentials."
}

save_pair() {
    local destination="$1"
    validate_pair_names && validate_pair_values || return 1
    (set -o noclobber; printf '%s' "$PAIR_PASSWORD" | jq -Rs --arg vault "$KEYVAULT_NAME" --arg tenant "$PAIR_TENANT_ID" --arg client "$PAIR_CLIENT_ID" --arg idName "$PAIR_ID_NAME" --arg secretName "$PAIR_SECRET_NAME" '{version:1,vault:$vault,tenantId:$tenant,clientId:$client,clientIdKeyVaultSecretName:$idName,clientSecretKeyVaultSecretName:$secretName,clientSecret:.}' > "$destination") || { pair_error "Cannot create protected handoff file (must not exist)"; return 1; }
    echo "Protected paired handoff prepared, not yet verified or published; upload it with --keyvault-only --secret-file, then delete it."
}

upload_pair() {
    local file="$1" expected_name="$2" mode snapshot
    [[ -f "$file" && ! -L "$file" && -O "$file" ]] || { pair_error "Handoff must be an owned regular file, not a symlink"; return 1; }
    mode=$(stat -c %a -- "$file") || return 1
    (( (8#$mode & 077) == 0 )) || { pair_error "Handoff must not be accessible by group or others"; return 1; }
    snapshot=$(mktemp "$PAIR_TEMP_DIR/handoff.XXXXXX") || return 1
    cat -- "$file" > "$snapshot" || { pair_error "Cannot read handoff"; return 1; }
    jq -se --arg vault "$KEYVAULT_NAME" --arg name "$expected_name" '
        length == 1 and (.[0] |
        type == "object" and .version == 1 and .vault == $vault and .clientSecretKeyVaultSecretName == $name
        and ([.tenantId, .clientId, .clientIdKeyVaultSecretName, .clientSecretKeyVaultSecretName, .clientSecret]
             | all(type == "string" and length > 0)))
    ' "$snapshot" >/dev/null 2>&1 || { pair_error "Invalid handoff or target does not match requested vault and secret"; return 1; }
    PAIR_TENANT_ID=$(jq -er '.tenantId' "$snapshot") || return 1
    PAIR_CLIENT_ID=$(jq -er '.clientId' "$snapshot") || return 1
    PAIR_ID_NAME=$(jq -er '.clientIdKeyVaultSecretName' "$snapshot") || return 1
    PAIR_SECRET_NAME=$(jq -er '.clientSecretKeyVaultSecretName' "$snapshot") || return 1
    PAIR_PASSWORD=$(jq -er '.clientSecret' "$snapshot") || return 1
    rm -f -- "$snapshot"
    publish_pair
}
