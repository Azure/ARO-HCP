#!/bin/bash
#
# Creates and configures service principals for the tenant-quota collector.
#
# This script manages the Azure AD service principal that the tenant-quota
# collector uses to query subscription quotas (ARM APIs) in the Microsoft test
# tenant. The RedHat0 principal (Graph consent, Reader on every ci.<env>
# subscription, Key Vault credential pair) is declarative and reconciled by the
# privileged entrypoint (make dev-ci-privileged-local-run).
#
# The test-tenant service principal needs:
# - Reader role on each target subscription for compute/network quota monitoring
#   and role assignment counting via the ARM authorization API
#
# The script reuses existing apps, permissions, role assignments, and Key Vault
# secrets when Azure lookups succeed. Lookup failures are treated as errors so
# only an explicit Key Vault "not found" result creates a new client secret.
#
# Prerequisites:
# - Azure CLI logged into the TARGET tenant where the SP should be created
# - Access to the opstool Key Vault (in the dev tenant) for storing secrets
#
# Multiple Azure identities (different users for target tenant vs Key Vault):
# - Log in both accounts (run az login twice, or use separate device codes).
# - Use "az account list -o table" and "az account set --subscription <id>" to pick
#   which login is active before each phase.
# - Or run the target-tenant phase with --skip-keyvault (creates client secret locally),
#   switch to an identity that can write to Key Vault, then upload with --keyvault-only.
#
# Usage:
#   ./scripts/manage-service-principals.sh --tenant test-tenant
#   ./scripts/manage-service-principals.sh --tenant test-tenant --skip-keyvault
#   ./scripts/manage-service-principals.sh --tenant test-tenant --skip-keyvault --defer-secret-out /safe/path/file
#   ./scripts/manage-service-principals.sh --keyvault-only --secret-name <name> --secret-file <path>
#   ./scripts/manage-service-principals.sh --keyvault-only --app-id <uuid> --secret-name <name>
#     (same-login only: resets credential in Entra then writes Key Vault)
#   ./scripts/manage-service-principals.sh --list
#
# After running, redeploy the collector to pick up any new secrets.
#

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/credential-pair.sh"

KEYVAULT_NAME="${OPSTOOL_KEYVAULT_NAME:-opstool-kv-usw3}"
CONFIG_PATH="$(dirname "${BASH_SOURCE[0]}")/../_output/config.yaml"

# When true, setup_* stops after Reader/Graph; use --keyvault-only after switching identity.
SKIP_KEYVAULT=false
# Upload from file (@--secret-file), or legacy reset+KV when --app-id is provided.
KEYVAULT_ONLY=false
KV_ONLY_APP_ID=""
KV_ONLY_SECRET_NAME=""
KV_ONLY_SECRET_FILE=""
# Optional path used with --skip-keyvault (--defer-secret-out PATH); omit for a secure mktemp file.
DEFER_SECRET_OUT=""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
print_success() { echo -e "${GREEN}[OK]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }
print_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
header()        { echo ""; echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"; echo -e "${BLUE}$1${NC}"; echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"; echo ""; }

print_error_details() {
    local details="$1"
    local shown=0

    while IFS= read -r line; do
        [[ -z "${line}" ]] && continue
        echo "  ${line}"
        shown=$((shown + 1))
        if [[ "${shown}" -ge 3 ]]; then
            break
        fi
    done <<< "${details}"
}

# =============================================================================
# TENANT DEFINITIONS
# =============================================================================
# Each function defines the SP name, permissions, and target subscriptions
# for a specific tenant. Subscriptions are referenced by display name and
# resolved to IDs at runtime.

# Test Test Azure Red Hat OpenShift (Microsoft test tenant). Subscription quota only.
setup_test_tenant() {
    local APPLICATION_NAME="custom-metrics-collector-test-test-azure-arohcp-ms-graph-ro"
    local SUBSCRIPTIONS=(
        "ARO HCP E2E"
        "ARO HCP E2E - Staging"
    )

    configure_test_tenant
    if [[ -n "$DEFER_SECRET_OUT" ]]; then validate_handoff_destination "$DEFER_SECRET_OUT"; fi
    create_or_get_sp "${APPLICATION_NAME}"
    grant_subscription_reader "${APP_ID}" "${SUBSCRIPTIONS[@]}"
    PAIR_CLIENT_ID="$APP_ID"
    if [[ "$SKIP_KEYVAULT" == true ]]; then
        prepare_password
    else
        store_secret
    fi
}

# =============================================================================
# HELPER FUNCTIONS
# =============================================================================

configure_test_tenant() {
    local tenant
    [[ -f "$CONFIG_PATH" ]] || { pair_error "Run make render-config first or supply --config"; exit 1; }
    tenant=$(yq -o=json '.tenants' "$CONFIG_PATH" | jq -ce '[.[] | select(.tenantName == "TestTestARO")] | if length == 1 then .[0] else error("TestTestARO must match exactly once") end')
    PAIR_TENANT_ID=$(printf '%s' "$tenant" | jq -er '.tenantId')
    PAIR_SECRET_NAME=$(printf '%s' "$tenant" | jq -er '.clientSecretKeyVaultSecretName')
    PAIR_ID_NAME=$(printf '%s' "$tenant" | jq -er '.clientIdKeyVaultSecretName')
    if [[ -n "$KV_ONLY_SECRET_NAME" ]]; then
        PAIR_SECRET_NAME="$KV_ONLY_SECRET_NAME"
        PAIR_ID_NAME="${PAIR_SECRET_NAME%-client-secret}-client-id"
    fi
    validate_pair_names || { pair_error "Invalid paired references"; exit 1; }
    [[ "$PAIR_TENANT_ID" =~ ^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$ ]] || { pair_error "Invalid configured tenant ID"; exit 1; }
    [[ "$(az account show --query tenantId -o tsv)" == "$PAIR_TENANT_ID" ]] || { pair_error "Login to the configured TestTestARO tenant before bootstrap"; exit 1; }
}

validate_handoff_destination() {
    [[ ! -e "$1" && ! -L "$1" && -d "$(dirname "$1")" && -w "$(dirname "$1")" ]] || { pair_error "Recovery handoff must be a new file in a writable directory; no password added"; exit 1; }
}

prepare_password() {
    RECOVERY_DIRECTORY=""
    RECOVERY_FILE="$DEFER_SECRET_OUT"
    if [[ -z "$RECOVERY_FILE" ]]; then
        RECOVERY_DIRECTORY=$(mktemp -d -t tenant-quota-bootstrap.XXXXXX)
        RECOVERY_FILE="$RECOVERY_DIRECTORY/pair.json"
    fi
    validate_handoff_destination "$RECOVERY_FILE"
    local operation_name
    operation_name="tenant-quota-$(basename "$PAIR_TEMP_DIR")"
    echo "Adding an application password with operation name $operation_name; retain this name for recovery."
    if ! PAIR_PASSWORD=$(az ad app credential reset --append --id "$PAIR_CLIENT_ID" --display-name "$operation_name" --years 1 --query password -o tsv 2>/dev/null); then
        pair_error "Password creation failed for operation $operation_name; inspect application credentials before retrying"
        exit 1
    fi
    if ! save_pair "$RECOVERY_FILE"; then
        pair_error "Cannot save recovery handoff for operation $operation_name; inspect this additive password before retrying; do not revoke unrelated credentials"
        exit 1
    fi
    echo "Recovery handoff: $RECOVERY_FILE"
}

create_or_get_sp() {
    local app_name="$1"
    local error_file
    local lookup_error

    error_file=$(mktemp)
    if APP_ID=$(az ad app list --display-name "${app_name}" --query "[?displayName=='${app_name}'].appId" -o tsv 2>"${error_file}"); then
        :
    else
        lookup_error=$(<"${error_file}")
        rm -f "${error_file}"
        print_error "Failed to look up application '${app_name}'"
        print_error_details "${lookup_error}"
        exit 1
    fi
    rm -f "${error_file}"

    if [[ "$APP_ID" == *$'\n'* || "$APP_ID" == *$'\t'* ]]; then
        pair_error "Ambiguous application display name"
        exit 1
    fi
    if [[ -n "${APP_ID}" ]]; then
        print_info "Application already exists"
    else
        header "Creating application: ${app_name}"
        local sp_output
        sp_output=$(az ad sp create-for-rbac \
            --create-password false \
            --display-name "${app_name}" \
            -o json)

        APP_ID=$(echo "${sp_output}" | jq -r '.appId')
        print_success "Created service principal"
    fi
}

grant_subscription_reader() {
    local app_id="$1"
    shift
    local subscriptions=("$@")

    header "Granting Reader role on subscriptions"

    for sub_name in "${subscriptions[@]}"; do
        local sub_id
        local error_file
        local lookup_error

        error_file=$(mktemp)
        if sub_id=$(az account list --query "[?name=='${sub_name}' && tenantId=='${PAIR_TENANT_ID}'].id" -o tsv 2>"${error_file}"); then
            :
        else
            lookup_error=$(<"${error_file}")
            rm -f "${error_file}"
            print_error "Failed to resolve subscription '${sub_name}'"
            print_error_details "${lookup_error}"
            exit 1
        fi
        rm -f "${error_file}"

        if [[ "$sub_id" == *$'\n'* || "$sub_id" == *$'\t'* ]]; then
            pair_error "Ambiguous subscription display name in configured tenant"
            exit 1
        fi
        if [[ -z "${sub_id}" ]]; then
            print_error "Could not find subscription '${sub_name}'"
            print_info "Make sure you have access to this subscription. You may need to:"
            echo "  az login --tenant <tenant-id>"
            exit 1
        fi

        print_info "Assigning Reader on '${sub_name}' (${sub_id})..."
        local output
        if output=$(az role assignment create \
            --assignee "${app_id}" \
            --role "Reader" \
            --scope "/subscriptions/${sub_id}" 2>&1); then
            print_success "Reader assigned"
        elif echo "${output}" | grep -qi "conflict\|already exists"; then
            print_info "Reader already assigned"
        else
            print_error "Failed to assign Reader role on '${sub_name}'"
            print_error_details "${output}"
            exit 1
        fi
    done
}

store_secret() {
    read_vault_value "$PAIR_ID_NAME"
    if [[ "$PAIR_FOUND" == true && "$PAIR_VALUE" != "$PAIR_CLIENT_ID" ]]; then
        pair_error "Identity changed: use new generation-specific secret names"
        exit 1
    fi
    read_vault_value "$PAIR_SECRET_NAME"
    if [[ "$PAIR_FOUND" == true ]]; then
        PAIR_PASSWORD="$PAIR_VALUE"
        publish_pair
    else
        prepare_password
        if ! publish_pair; then
            pair_error "Publication incomplete; do not bootstrap again or restart" || true
            printf 'Retry the saved handoff: %q --keyvault-only --keyvault %q --secret-name %q --secret-file %q\n' "$(dirname "${BASH_SOURCE[0]}")/manage-service-principals.sh" "$KEYVAULT_NAME" "$PAIR_SECRET_NAME" "$RECOVERY_FILE" >&2
            exit 1
        fi
        rm -f -- "$RECOVERY_FILE"
        if [[ -n "$RECOVERY_DIRECTORY" ]]; then rmdir -- "$RECOVERY_DIRECTORY"; fi
    fi
}

keyvault_upload_secret_from_file() {
    upload_pair "$2" "$1"
}

list_tenants() {
    header "Available tenant configurations"
    echo "  test-tenant     - Test Test Azure Red Hat OpenShift (subscriptions only; no Graph directory quota)"
    echo ""
    echo "Usage:"
    echo "  $0 --tenant test-tenant"
    echo ""
    echo "Prerequisites:"
    echo "  Login to the TARGET tenant before running:"
    echo "    test-tenant:  az login --tenant <tenant-id>"
    echo ""
    echo "  Different users for target tenant vs Key Vault (common for test tenants):"
    echo "    1) az login → identity that owns the SERVICE PRINCIPAL TIER (Azure AD / subscriptions)"
    echo "    2) $0 --tenant <name> --skip-keyvault [--defer-secret-out PATH]"
    echo "       (writes a chmod 600 paired JSON handoff file unless you pick PATH)"
    echo "    3) az login → identity that can WRITE to '${KEYVAULT_NAME}'"
    echo "       az account set --subscription \"<subscription-with-keyvault-access>\""
    echo "    4) $0 --keyvault-only --secret-name '<name>' --secret-file '<printed-path>'"
    echo ""
    echo "  Same user for Entra app + Key Vault (no defer file):"
    echo "    $0 --keyvault-only --app-id '<uuid>' --secret-name '<name>'"
    echo ""
    echo "  Or keep both logins cached and swap only:"
    echo "    az account set --subscription '<sub-in-chosen-tenant>'"
}

usage() {
    echo "Usage: $0 [--tenant NAME] [--skip-keyvault] [--keyvault-only ...] [--list | --help]"
    echo ""
    echo "Creates and configures service principals for the tenant-quota collector."
    echo ""
    echo "Options:"
    echo "  --tenant NAME       Setup SP for the named tenant (test-tenant)"
    echo "  --config PATH       Rendered collector config (default: ../_output/config.yaml)"
    echo "  --skip-keyvault     Stop after subscriptions/permissions; prepare a protected credential handoff for verification during upload"
    echo "  --defer-secret-out  With --skip-keyvault: path for the paired JSON handoff file (default: mktemp)"
    echo "  --keyvault-only     Only Key Vault: upload --secret-file, or reset+KV with --app-id"
    echo "  --app-id ID         Application (client) id for --keyvault-only (same-tenant reset path)"
    echo "  --secret-name NAME  Key Vault secret name"
    echo "  --secret-file PATH  With --keyvault-only: upload paired JSON handoff (no credential reset)"
    echo "  --list              List available tenant configurations"
    echo "  --keyvault NAME     Override Key Vault name (default: ${KEYVAULT_NAME})"
    echo "  --help              Show this help message"
}

# =============================================================================
# MAIN
# =============================================================================

TENANT=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --tenant)
            TENANT="$2"
            shift 2
            ;;
        --keyvault)
            KEYVAULT_NAME="$2"
            shift 2
            ;;
        --config)
            CONFIG_PATH="$2"
            shift 2
            ;;
        --list)
            list_tenants
            exit 0
            ;;
        --skip-keyvault)
            SKIP_KEYVAULT=true
            shift
            ;;
        --keyvault-only)
            KEYVAULT_ONLY=true
            shift
            ;;
        --app-id)
            KV_ONLY_APP_ID="$2"
            shift 2
            ;;
        --secret-name)
            KV_ONLY_SECRET_NAME="$2"
            shift 2
            ;;
        --secret-file)
            KV_ONLY_SECRET_FILE="$2"
            shift 2
            ;;
        --defer-secret-out)
            DEFER_SECRET_OUT="$2"
            shift 2
            ;;
        --help)
            usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

if [[ -n "${DEFER_SECRET_OUT}" && "${SKIP_KEYVAULT}" != "true" ]]; then
    print_error "--defer-secret-out requires --skip-keyvault"
    usage
    exit 1
fi

if [[ -n "${KV_ONLY_SECRET_FILE}" && "${KEYVAULT_ONLY}" != "true" ]]; then
    print_error "--secret-file requires --keyvault-only"
    usage
    exit 1
fi

if [[ "${SKIP_KEYVAULT}" == "true" && "${KEYVAULT_ONLY}" == "true" ]]; then
    print_error "Cannot combine --skip-keyvault and --keyvault-only"
    usage
    exit 1
fi

if [[ "${KEYVAULT_ONLY}" == "true" ]]; then
    if [[ -n "${KV_ONLY_SECRET_FILE}" ]]; then
        if [[ -z "${KV_ONLY_SECRET_NAME}" ]]; then
            print_error "--keyvault-only with --secret-file requires --secret-name"
            usage
            exit 1
        fi
        if [[ -n "${KV_ONLY_APP_ID}" ]]; then
            print_warning "--app-id is ignored when --secret-file is set"
        fi

        keyvault_upload_secret_from_file "${KV_ONLY_SECRET_NAME}" "${KV_ONLY_SECRET_FILE}"
    else
        if [[ -z "${KV_ONLY_APP_ID}" || -z "${KV_ONLY_SECRET_NAME}" ]]; then
            print_error "--keyvault-only requires (--secret-file and --secret-name) or (--app-id and --secret-name)"
            usage
            exit 1
        fi

        header "Key Vault phase only (Entra reset + upload)"
        print_info "Key Vault: ${KEYVAULT_NAME}"
        configure_test_tenant
        PAIR_CLIENT_ID="$KV_ONLY_APP_ID"
        store_secret
    fi

    header "Done"
    echo "Next steps:"
    echo "  1. If this is a new tenant, add it to config/config-dev-ci.yaml"
    echo "  2. Redeploy the collector (see templatize pipeline in README / script footer)."
    exit 0
fi

if [[ -z "${TENANT}" ]]; then
    if [[ "${SKIP_KEYVAULT}" == "true" ]]; then
        print_error "--skip-keyvault requires --tenant <name>"
        usage
        exit 1
    fi
    list_tenants
    exit 0
fi

case "${TENANT}" in
    test-tenant|test_tenant|TestTenant)
        setup_test_tenant
        ;;
    *)
        print_error "Unknown tenant: ${TENANT}"
        list_tenants
        exit 1
        ;;
esac

header "Done"
echo "Next steps:"
echo "  1. If this is a new tenant, add it to config/config-dev-ci.yaml"
echo "  2. Redeploy the collector:"
echo "     ./templatize-bin pipeline run \\"
echo "       --service-group Microsoft.Azure.ARO.HCP.DevCI.TenantQuota \\"
echo "       --topology-file topology-dev-ci.yaml \\"
echo "       --config-file config/config-dev-ci.yaml \\"
echo "       --dev-settings-file tooling/templatize/settings.yaml \\"
echo "       --dev-environment dev-ci \\"
echo "       --step deploy"
