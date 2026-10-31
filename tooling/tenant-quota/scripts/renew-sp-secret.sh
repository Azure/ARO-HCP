#!/bin/bash

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/credential-pair.sh"

KEYVAULT_NAME="${OPSTOOL_KEYVAULT_NAME:-opstool-kv-usw3}"
NAMESPACE="${OPSTOOL_NAMESPACE:-tenant-quota}"
DEPLOYMENT_NAME="${OPSTOOL_DEPLOYMENT:-tenant-quota-collector}"
CONFIG_PATH="$(dirname "${BASH_SOURCE[0]}")/../_output/config.yaml"
TENANT_NAME=""
EXPIRY_YEARS=1
RESTART=false
CONFIRM=true
LIST=false
DEFER_SECRET_OUT=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --tenant) TENANT_NAME="$2"; shift 2 ;;
        --config) CONFIG_PATH="$2"; shift 2 ;;
        --keyvault) KEYVAULT_NAME="$2"; shift 2 ;;
        --namespace) NAMESPACE="$2"; shift 2 ;;
        --expiry) EXPIRY_YEARS="$2"; shift 2 ;;
        --defer-secret-out) DEFER_SECRET_OUT="$2"; shift 2 ;;
        --restart) RESTART=true; shift ;;
        --yes|-y) CONFIRM=false; shift ;;
        --list) LIST=true; shift ;;
        --help) echo "Usage: $0 --tenant NAME [--config rendered-config.yaml] [--expiry YEARS] [--keyvault VAULT] [--defer-secret-out FILE | --restart] [--yes] | --list"; exit 0 ;;
        *) pair_error "Unknown option: $1"; exit 1 ;;
    esac
done

[[ "$EXPIRY_YEARS" =~ ^[1-9][0-9]*$ ]] || { pair_error "Expiry must be positive years"; exit 1; }
[[ -f "$CONFIG_PATH" ]] || { pair_error "Run make render-config first or supply --config"; exit 1; }
if [[ "$LIST" == true ]]; then
    yq -r '.tenants[].tenantName' "$CONFIG_PATH"
    exit 0
fi
[[ -n "$TENANT_NAME" ]] || { pair_error "--tenant is required"; exit 1; }
[[ -z "$DEFER_SECRET_OUT" || "$RESTART" == false ]] || { pair_error "Deferred publication cannot restart"; exit 1; }
tenant=$(yq -o=json '.tenants' "$CONFIG_PATH" | jq -ce --arg name "$TENANT_NAME" '[.[] | select(.tenantName == $name)] | if length == 1 then .[0] else error("Tenant must match exactly once") end')
PAIR_TENANT_ID=$(printf '%s' "$tenant" | jq -er '.tenantId')
PAIR_ID_NAME=$(printf '%s' "$tenant" | jq -er '.clientIdKeyVaultSecretName')
PAIR_SECRET_NAME=$(printf '%s' "$tenant" | jq -er '.clientSecretKeyVaultSecretName')
validate_pair_names || { pair_error "Invalid paired references"; exit 1; }
read_vault_value "$PAIR_ID_NAME"
[[ "$PAIR_FOUND" == true ]] || { pair_error "Publish a verified client ID binding before renewal"; exit 1; }
PAIR_CLIENT_ID="$PAIR_VALUE"
read_vault_value "$PAIR_SECRET_NAME"
[[ "$PAIR_FOUND" == true ]] || { pair_error "Missing client secret"; exit 1; }
PAIR_PASSWORD="$PAIR_VALUE"
verify_pair
[[ "$(az account show --query tenantId -o tsv)" == "$PAIR_TENANT_ID" ]] || { pair_error "Login to the configured application tenant before renewal"; exit 1; }
if [[ "$CONFIRM" == true ]]; then
    read -r -p "Add a password and publish the verified pair? [y/N] " answer
    [[ "$answer" == y || "$answer" == Y ]] || exit 0
fi
recovery_file="$DEFER_SECRET_OUT"
recovery_directory=""
if [[ -z "$recovery_file" ]]; then
    recovery_directory=$(mktemp -d -t tenant-quota-renewal.XXXXXX)
    recovery_file="$recovery_directory/pair.json"
fi
[[ ! -e "$recovery_file" && ! -L "$recovery_file" && -d "$(dirname "$recovery_file")" && -w "$(dirname "$recovery_file")" ]] || { pair_error "Recovery handoff must be a new file in a writable directory; no password added"; exit 1; }
operation_name="tenant-quota-$(basename "$PAIR_TEMP_DIR")"
echo "Adding an application password with operation name $operation_name; retain this name for recovery."
if ! PAIR_PASSWORD=$(az ad app credential reset --append --id "$PAIR_CLIENT_ID" --display-name "$operation_name" --years "$EXPIRY_YEARS" --query password -o tsv 2>/dev/null); then
    pair_error "Password creation failed for operation $operation_name; inspect application credentials before retrying"
    exit 1
fi
if ! save_pair "$recovery_file"; then
    pair_error "Cannot save recovery handoff for operation $operation_name; inspect this additive password before retrying; do not revoke unrelated credentials"
    exit 1
fi
echo "Recovery handoff: $recovery_file"
if [[ -n "$DEFER_SECRET_OUT" ]]; then
    exit 0
fi
if ! publish_pair; then
    pair_error "Publication incomplete for operation $operation_name; do not renew again or restart" || true
    printf 'Retry the saved handoff: %q --keyvault-only --keyvault %q --secret-name %q --secret-file %q\n' "$(dirname "${BASH_SOURCE[0]}")/manage-service-principals.sh" "$KEYVAULT_NAME" "$PAIR_SECRET_NAME" "$recovery_file" >&2
    exit 1
fi
rm -f -- "$recovery_file"
if [[ -n "$recovery_directory" ]]; then rmdir -- "$recovery_directory"; fi
if [[ "$RESTART" == true ]]; then
    kubectl rollout restart "deployment/$DEPLOYMENT_NAME" -n "$NAMESPACE"
    kubectl rollout status "deployment/$DEPLOYMENT_NAME" -n "$NAMESPACE" --timeout=120s
else
    echo "REQUIRED: restart deployment/$DEPLOYMENT_NAME in $NAMESPACE after publication and mount verification."
fi
echo "Verify fresh successful collection before revoking old passwords; health probes and retained gauges are insufficient."
