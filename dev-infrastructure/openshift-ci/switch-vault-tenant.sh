#!/bin/bash
set -euo pipefail

# Script to switch aro-hcp-stg and aro-hcp-prod Vault secrets between tenants
#
# Usage:
#   ./switch-vault-tenant.sh --to msft       # Switch to Test Test Azure Red Hat OpenShift tenant
#   ./switch-vault-tenant.sh --to rh-tenant  # Switch back to Red Hat tenant (rollback)
#   ./switch-vault-tenant.sh --status        # Show current tenant status
#
# This script copies credentials from source secrets to the active secrets:
#   - msft:      aro-hcp-{env}-msft     → aro-hcp-{env}
#   - rh-tenant: aro-hcp-{env}-rh-tenant → aro-hcp-{env}

VAULT_URL="https://vault.ci.openshift.org"
VAULT_BASE_PATH="selfservice/hcm-aro"

TARGET=""
SHOW_STATUS=false
TARGET_ENV=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --to)
            TARGET="$2"
            if [[ "$TARGET" != "msft" && "$TARGET" != "rh-tenant" ]]; then
                echo "Error: --to must be 'msft' or 'rh-tenant'"
                exit 1
            fi
            shift 2
            ;;
        --status)
            SHOW_STATUS=true
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
            echo "Usage: $0 --to <msft|rh-tenant> [--env stg|prod]"
            echo "       $0 --status"
            echo ""
            echo "Options:"
            echo "  --to TARGET   Switch to specified tenant (msft or rh-tenant)"
            echo "  --status      Show current tenant status"
            echo "  --env ENV     Only switch specific environment (stg or prod)"
            echo ""
            echo "Examples:"
            echo "  $0 --to msft           # Switch both envs to MSFT tenant"
            echo "  $0 --to rh-tenant      # Rollback both envs to Red Hat tenant"
            echo "  $0 --to msft --env stg # Switch only STAGE to MSFT tenant"
            echo "  $0 --status            # Check current tenant for each env"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

if [[ "${SHOW_STATUS}" == "false" && -z "${TARGET}" ]]; then
    echo "Error: Must specify --to <msft|rh-tenant> or --status"
    echo "Run '$0 --help' for usage"
    exit 1
fi

header() {
    echo ""
    echo "=========================================="
    echo "$1"
    echo "=========================================="
}

get_tenant_info() {
    local env="$1"
    local secret_path="kv/${VAULT_BASE_PATH}/aro-hcp-${env}"
    
    local tenant_id=$(vault kv get -format=json "${secret_path}" 2>/dev/null | jq -r '.data.data.tenant // "unknown"')
    local subscription_name=$(vault kv get -format=json "${secret_path}" 2>/dev/null | jq -r '.data.data["subscription-name"] // "unknown"')
    
    echo "${tenant_id}|${subscription_name}"
}

show_status() {
    header "Current Tenant Status"
    
    echo ""
    printf "%-6s | %-40s | %s\n" "ENV" "TENANT ID" "SUBSCRIPTION"
    printf "%-6s-+-%-40s-+-%s\n" "------" "----------------------------------------" "--------------------"
    
    for env in stg prod; do
        local info=$(get_tenant_info "${env}")
        local tenant_id="${info%%|*}"
        local subscription_name="${info#*|}"
        
        local tenant_name="unknown"
        if [[ "${tenant_id}" == "93b21e64-4824-439a-b893-46c9b2a51082" ]]; then
            tenant_name="Test Test Azure Red Hat OpenShift"
        elif [[ "${tenant_id}" != "unknown" ]]; then
            tenant_name="Red Hat"
        fi
        
        printf "%-6s | %-40s | %s (%s)\n" "${env}" "${tenant_id}" "${subscription_name}" "${tenant_name}"
    done
    echo ""
}

copy_secret() {
    local env="$1"
    local source_suffix="$2"
    
    local source_path="kv/${VAULT_BASE_PATH}/aro-hcp-${env}-${source_suffix}"
    local target_path="kv/${VAULT_BASE_PATH}/aro-hcp-${env}"
    
    echo "Checking source secret: ${source_path}"
    if ! vault kv get "${source_path}" > /dev/null 2>&1; then
        echo "  Error: Source secret does not exist: ${source_path}"
        return 1
    fi
    
    echo "Reading source secret..."
    local client_id=$(vault kv get -format=json "${source_path}" | jq -r '.data.data["client-id"]')
    local client_secret=$(vault kv get -format=json "${source_path}" | jq -r '.data.data["client-secret"]')
    local tenant=$(vault kv get -format=json "${source_path}" | jq -r '.data.data.tenant')
    local subscription_id=$(vault kv get -format=json "${source_path}" | jq -r '.data.data["subscription-id"]')
    local subscription_name=$(vault kv get -format=json "${source_path}" | jq -r '.data.data["subscription-name"]')
    
    echo "  Tenant: ${tenant}"
    echo "  Subscription: ${subscription_name}"
    
    # Use patch to only update credential fields, preserving secretsync fields
    echo "Patching target secret: ${target_path}"
    vault kv patch "${target_path}" \
        client-id="${client_id}" \
        client-secret="${client_secret}" \
        tenant="${tenant}" \
        subscription-id="${subscription_id}" \
        subscription-name="${subscription_name}"
    
    unset client_secret
    
    echo "  ✓ Patched ${target_path} with credentials from ${source_path}"
}

# Check prerequisites
if ! command -v vault &> /dev/null; then
    echo "Error: vault CLI is not installed"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "Error: jq is not installed"
    exit 1
fi

# Login to Vault (skip if already logged in)
export VAULT_ADDR="${VAULT_URL}"
if vault token lookup > /dev/null 2>&1; then
    echo "Already logged into Vault"
else
    echo "Logging into Vault (browser will open)..."
    vault login --method=oidc > /dev/null 2>&1
    echo "Successfully logged into Vault"
fi

# Show status if requested
if [[ "${SHOW_STATUS}" == "true" ]]; then
    show_status
    exit 0
fi

# Determine source suffix based on target
if [[ "${TARGET}" == "msft" ]]; then
    SOURCE_SUFFIX="msft"
    TARGET_NAME="Test Test Azure Red Hat OpenShift tenant"
else
    SOURCE_SUFFIX="rh-tenant"
    TARGET_NAME="Red Hat tenant (rollback)"
fi

# Determine which environments to switch
if [[ -n "${TARGET_ENV}" ]]; then
    ENVS=("${TARGET_ENV}")
else
    ENVS=("stg" "prod")
fi

header "Switching to ${TARGET_NAME}"

echo ""
echo "This will update the following secrets:"
for env in "${ENVS[@]}"; do
    echo "  aro-hcp-${env}-${SOURCE_SUFFIX} → aro-hcp-${env}"
done
echo ""

# Perform the switch
for env in "${ENVS[@]}"; do
    header "Switching: aro-hcp-${env}"
    copy_secret "${env}" "${SOURCE_SUFFIX}"
done

header "Switch Complete"

echo ""
echo "Switched to: ${TARGET_NAME}"
echo ""
echo "Updated secrets:"
for env in "${ENVS[@]}"; do
    echo "  - kv/${VAULT_BASE_PATH}/aro-hcp-${env}"
done
echo ""
echo "Changes will propagate to Prow jobs in 5-10 minutes."
echo ""
echo "To verify the current status:"
echo "  $0 --status"
echo ""
if [[ "${TARGET}" == "msft" ]]; then
    echo "To test:"
    echo "  /test stage-e2e-parallel (in Azure/ARO-HCP repo)"
    echo ""
    echo "To rollback if needed:"
    echo "  $0 --to rh-tenant"
else
    echo "Rollback complete. Original Red Hat tenant credentials are now active."
fi

