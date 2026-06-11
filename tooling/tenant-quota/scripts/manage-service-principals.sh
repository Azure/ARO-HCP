#!/bin/bash
#
# Creates and configures service principals for the tenant-quota collector.
#
# This script manages the Azure AD service principals that the tenant-quota
# collector uses to query directory quotas (Graph API) and subscription
# quotas (ARM APIs) across multiple tenants.
#
# Each tenant needs its own service principal with:
# - Directory.Read.All (Graph API) for directory quota monitoring
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
#   ./scripts/manage-service-principals.sh --tenant redhat
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

KEYVAULT_NAME="${OPSTOOL_KEYVAULT_NAME:-opstool-kv-usw3}"

# When true, setup_* stops after Reader/Graph; use --keyvault-only after switching identity.
SKIP_KEYVAULT=false
# Upload from file (@--secret-file), or legacy reset+KV when --app-id is provided.
KEYVAULT_ONLY=false
KV_ONLY_APP_ID=""
KV_ONLY_SECRET_NAME=""
KV_ONLY_SECRET_FILE=""
# Optional path used with --skip-keyvault (--defer-secret-out PATH); omit for a secure mktemp file.
DEFER_SECRET_OUT=""
# One-time password from az ad sp create-for-rbac (avoid an extra credential reset on first deploy).
SP_BOOTSTRAP_PASSWORD=""

GRAPH_APP_ID="00000003-0000-0000-c000-000000000000"
# Organization.Read.All application permission (for /v1.0/organization directory quota)
ORGANIZATION_READ_ALL="498476ce-e0fe-48b0-b801-37ba7e2685c6"

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

is_secret_not_found_error() {
    local details="$1"

    case "${details}" in
        *SecretNotFound*|*"was not found in this key vault"*)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

# =============================================================================
# TENANT DEFINITIONS
# =============================================================================
# Each function defines the SP name, permissions, and target subscriptions
# for a specific tenant. Subscriptions are referenced by display name and
# resolved to IDs at runtime.

setup_redhat() {
    local APPLICATION_NAME="custom-metrics-collector-redhat0-ms-graph-ro"
    local KEYVAULT_SECRET_NAME="custom-metrics-collector-redhat0-client-secret"
    local DIRECTORY_QUOTA=true
    local SUBSCRIPTIONS=(
        "ARO Hosted Control Planes (EA Subscription 1)"
        "ARO HCP E2E Infrastructure (EA Subscription)"
        "ARO HCP E2E Hosted Clusters (EA Subscription)"
        "ARO HCP E2E Hosted Clusters 2 (EA Subscription)"
        "ARO HCP E2E Hosted Clusters - Dev - 02"
        "ARO HCP E2E Hosted Clusters - Dev - 03"
        "ARO SRE Team - INT (EA Subscription 3)"
    )

    create_or_get_sp "${APPLICATION_NAME}"
    grant_graph_permissions "${APP_ID}" "${DIRECTORY_QUOTA}"
    grant_subscription_reader "${APP_ID}" "${SUBSCRIPTIONS[@]}"
    maybe_store_secret "${APP_ID}" "${KEYVAULT_SECRET_NAME}"
}

# Test Test Azure Red Hat OpenShift (Microsoft test tenant).
# Subscription quota only — no Graph / Organization.Read.All (directory quota not used).
setup_test_tenant() {
    local APPLICATION_NAME="custom-metrics-collector-test-test-azure-arohcp-ms-graph-ro"
    local KEYVAULT_SECRET_NAME="custom-metrics-collector-test-test-azure-arohcp-client-secret"
    local DIRECTORY_QUOTA=false
    local SUBSCRIPTIONS=(
        "ARO HCP E2E"
        "ARO HCP E2E Hosted Clusters - Stage - 00"
    )

    create_or_get_sp "${APPLICATION_NAME}"
    grant_graph_permissions "${APP_ID}" "${DIRECTORY_QUOTA}"
    grant_subscription_reader "${APP_ID}" "${SUBSCRIPTIONS[@]}"
    maybe_store_secret "${APP_ID}" "${KEYVAULT_SECRET_NAME}"
}

# =============================================================================
# HELPER FUNCTIONS
# =============================================================================

maybe_store_secret() {
    local app_id="$1"
    local secret_name="$2"

    if [[ "${SKIP_KEYVAULT}" != "true" ]]; then
        store_secret "${app_id}" "${secret_name}"
        return 0
    fi

    header "Deferring Key Vault; preparing client secret in target tenant"
    local new_secret=""

    if [[ -n "${SP_BOOTSTRAP_PASSWORD}" ]]; then
        new_secret="${SP_BOOTSTRAP_PASSWORD}"
        SP_BOOTSTRAP_PASSWORD=""
        print_info "Using the initial password from az ad sp create-for-rbac (no extra credential reset)."
    else
        print_info "Creating a client secret with az ad sp credential reset (requires app admin in this tenant)..."
        if ! new_secret=$(create_sp_client_password "${app_id}"); then
            print_error "Failed to create client secret"
            exit 1
        fi
    fi

    if [[ -z "${new_secret}" ]]; then
        print_error "Client secret value is empty"
        exit 1
    fi

    local outpath="${DEFER_SECRET_OUT}"
    if [[ -z "${outpath}" ]]; then
        outpath=$(mktemp -t tenant-quota-kv-defer.XXXXXX)
    fi

    printf '%s\n' "${new_secret}" > "${outpath}"
    chmod 600 "${outpath}"

    print_success "Wrote client secret (one line) to: ${outpath}"
    print_warning "Protect this file; delete it after Key Vault upload (e.g. shred -u or rm)."
    print_info "Application (client) ID: ${app_id}"
    print_info "Key Vault secret name: ${secret_name}"
    echo ""
    print_info "Switch to the identity that can write to Key Vault '${KEYVAULT_NAME}', then run:"
    echo "  az account set --subscription \"<subscription-with-keyvault-access>\""
    echo "  $0 --keyvault-only --secret-name ${secret_name} --secret-file ${outpath}"
    echo ""
}

create_or_get_sp() {
    local app_name="$1"
    local error_file
    local lookup_error

    SP_BOOTSTRAP_PASSWORD=""

    error_file=$(mktemp)
    if APP_ID=$(az ad app list --display-name "${app_name}" --query '[0].appId' -o tsv 2>"${error_file}"); then
        :
    else
        lookup_error=$(<"${error_file}")
        rm -f "${error_file}"
        print_error "Failed to look up application '${app_name}'"
        print_error_details "${lookup_error}"
        exit 1
    fi
    rm -f "${error_file}"

    if [[ -n "${APP_ID}" ]]; then
        print_info "Application '${app_name}' already exists: ${APP_ID}"
    else
        header "Creating application: ${app_name}"
        local sp_output
        sp_output=$(az ad sp create-for-rbac \
            --years 10 \
            --display-name "${app_name}" \
            -o json)

        APP_ID=$(echo "${sp_output}" | jq -r '.appId')
        SP_BOOTSTRAP_PASSWORD=$(echo "${sp_output}" | jq -r '.password // empty')
        if [[ "${SP_BOOTSTRAP_PASSWORD}" == "null" ]]; then
            SP_BOOTSTRAP_PASSWORD=""
        fi
        print_success "Created service principal: ${APP_ID}"
    fi
}

grant_graph_permissions() {
    local app_id="$1"
    local directory_quota="$2"

    if [[ "${directory_quota}" != "true" ]]; then
        print_info "Skipping Graph API permissions (directoryQuota=false)"
        return 0
    fi

    header "Granting Graph API permissions"

    local existing
    local error_file
    local permission_error

    error_file=$(mktemp)
    if existing=$(az rest --method GET \
        --url "https://graph.microsoft.com/v1.0/servicePrincipals(appId='${app_id}')/appRoleAssignments" \
        --query "value[].appRoleId" -o tsv 2>"${error_file}"); then
        :
    else
        permission_error=$(<"${error_file}")
        rm -f "${error_file}"
        print_error "Failed to query existing Graph API permissions for '${app_id}'"
        print_error_details "${permission_error}"
        exit 1
    fi
    rm -f "${error_file}"

    if echo "${existing}" | grep -q "${ORGANIZATION_READ_ALL}"; then
        print_info "Organization.Read.All already granted"
    else
        print_info "Adding Organization.Read.All permission..."
        az ad app permission add \
            --id "${app_id}" \
            --api "${GRAPH_APP_ID}" \
            --api-permissions "${ORGANIZATION_READ_ALL}=Role"

        print_info "Requesting admin consent (requires tenant admin)..."
        az ad app permission admin-consent --id "${app_id}"
        print_success "Graph API permissions granted"
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
        if sub_id=$(az account list --query "[?name=='${sub_name}'].id" -o tsv 2>"${error_file}"); then
            :
        else
            lookup_error=$(<"${error_file}")
            rm -f "${error_file}"
            print_error "Failed to resolve subscription '${sub_name}'"
            print_error_details "${lookup_error}"
            exit 1
        fi
        rm -f "${error_file}"

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

create_sp_client_password() {
    local app_id="$1"

    az ad sp credential reset \
        --id "${app_id}" \
        --display-name "tenant-quota-collector-initial" \
        --years 1 \
        --query password \
        -o tsv
}

store_secret() {
    local app_id="$1"
    local secret_name="$2"
    local existing_secret
    local error_file
    local secret_error

    header "Checking Key Vault secret"

    error_file=$(mktemp)
    if existing_secret=$(az keyvault secret show \
        --vault-name "${KEYVAULT_NAME}" \
        --name "${secret_name}" \
        --query "value" -o tsv 2>"${error_file}"); then
        rm -f "${error_file}"
        if [[ -z "${existing_secret}" ]]; then
            print_error "Key Vault secret '${secret_name}' exists but returned an empty value"
            exit 1
        fi
    else
        secret_error=$(<"${error_file}")
        rm -f "${error_file}"
        if ! is_secret_not_found_error "${secret_error}"; then
            print_error "Failed to query Key Vault secret '${secret_name}'"
            print_error_details "${secret_error}"
            exit 1
        fi
        existing_secret=""
    fi

    if [[ -n "${existing_secret}" ]]; then
        print_info "Key Vault secret '${secret_name}' already exists"
        print_info "To rotate, use: ./scripts/renew-sp-secret.sh --tenant <name>"
        return 0
    fi

    print_info "Key Vault secret '${secret_name}' was not found"
    print_info "Creating new client secret and storing in Key Vault..."
    local new_secret
    if [[ -n "${SP_BOOTSTRAP_PASSWORD}" ]]; then
        new_secret="${SP_BOOTSTRAP_PASSWORD}"
        SP_BOOTSTRAP_PASSWORD=""
        print_info "Using the initial password from az ad sp create-for-rbac (no credential reset)."
    elif ! new_secret=$(create_sp_client_password "${app_id}"); then
        print_error "Failed to create client secret"
        exit 1
    fi

    if [[ -z "${new_secret}" ]]; then
        print_error "Failed to create client secret"
        exit 1
    fi

    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "${secret_name}" \
        --value "${new_secret}" \
        --description "Created $(date +%Y-%m-%d) by manage-service-principals.sh" \
        > /dev/null

    print_success "Secret stored in Key Vault as '${secret_name}'"
}

keyvault_upload_secret_from_file() {
    local secret_name="$1"
    local file_path="$2"
    local new_secret

    header "Uploading secret to Key Vault (no Entra credential reset)"

    if [[ ! -f "${file_path}" || ! -r "${file_path}" ]]; then
        print_error "Secret file not found or not readable: ${file_path}"
        exit 1
    fi
    IFS= read -r new_secret < "${file_path}" || true
    new_secret="${new_secret//$'\r'/}"
    if [[ -z "${new_secret}" ]]; then
        print_error "Secret file is empty (first line): ${file_path}"
        exit 1
    fi

    print_info "Key Vault: ${KEYVAULT_NAME}"
    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "${secret_name}" \
        --value "${new_secret}" \
        --description "Uploaded $(date +%Y-%m-%d) by manage-service-principals.sh (--secret-file)" \
        > /dev/null

    print_success "Secret stored in Key Vault as '${secret_name}'"
    print_warning "Remove the local secret file when finished (e.g. shred -u or rm -f)."
}

list_tenants() {
    header "Available tenant configurations"
    echo "  redhat          - RedHat0 tenant (dev/int/e2e subscriptions)"
    echo "  test-tenant     - Test Test Azure Red Hat OpenShift (subscriptions only; no Graph directory quota)"
    echo ""
    echo "Usage:"
    echo "  $0 --tenant redhat"
    echo "  $0 --tenant test-tenant"
    echo ""
    echo "Prerequisites:"
    echo "  Login to the TARGET tenant before running:"
    echo "    RedHat0:      az login --tenant 64dc69e4-d083-49fc-9569-ebece1dd1408"
    echo "    test-tenant:  az login --tenant <tenant-id>"
    echo ""
    echo "  Different users for target tenant vs Key Vault (common for test tenants):"
    echo "    1) az login → identity that owns the SERVICE PRINCIPAL TIER (Azure AD / subscriptions)"
    echo "    2) $0 --tenant <name> --skip-keyvault [--defer-secret-out PATH]"
    echo "       (writes a chmod 600 one-line secret file unless you pick PATH)"
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
    echo "  --tenant NAME       Setup SP for the named tenant (redhat, test-tenant)"
    echo "  --skip-keyvault     Stop after subscriptions/permissions; write client secret to a local file"
    echo "  --defer-secret-out  With --skip-keyvault: path for the one-line secret file (default: mktemp)"
    echo "  --keyvault-only     Only Key Vault: upload --secret-file, or reset+KV with --app-id"
    echo "  --app-id ID         Application (client) id for --keyvault-only (same-tenant reset path)"
    echo "  --secret-name NAME  Key Vault secret name"
    echo "  --secret-file PATH  With --keyvault-only: upload first line (no az ad sp credential reset)"
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
        store_secret "${KV_ONLY_APP_ID}" "${KV_ONLY_SECRET_NAME}"
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
    redhat|RedHat0|redhat0)
        setup_redhat
        ;;
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
