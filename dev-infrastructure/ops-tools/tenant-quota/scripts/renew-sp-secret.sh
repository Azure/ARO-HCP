#!/bin/bash
#
# Renews the client secret for a tenant-quota service principal
#
# This script:
# 1. Shows current secret expiration dates
# 2. Creates a new client secret in Azure AD
# 3. Updates the Key Vault secret
# 4. Optionally restarts the collector pod to pick up the new secret
#
# Prerequisites:
# - Azure CLI logged in with permissions to manage the service principal
# - Access to the Key Vault
#
# Usage (from tenant-quota directory or repo root):
#   ./scripts/renew-sp-secret.sh [--tenant TENANT_NAME] [--expiry YEARS] [--restart]
#
# Examples:
#   ./scripts/renew-sp-secret.sh                    # Interactive mode
#   ./scripts/renew-sp-secret.sh --tenant RedHat0   # Renew RedHat0 tenant SP
#   ./scripts/renew-sp-secret.sh --tenant RedHat0 --expiry 1 --restart
#
# Environment Variables (override defaults):
#   OPSTOOL_KEYVAULT_NAME     Key Vault name (default: opstool-kv-usw3)
#   OPSTOOL_RESOURCE_GROUP    Resource group (default: opstool-westus3)
#   OPSTOOL_AKS_CLUSTER       AKS cluster name (default: opstool-wus3)
#   OPSTOOL_NAMESPACE         Kubernetes namespace (default: tenant-quota)
#   OPSTOOL_DEPLOYMENT        Deployment name (default: tenant-quota-collector)
#

set -eo pipefail

# =============================================================================
# CONFIGURATION
# =============================================================================
# These can be overridden via environment variables or command-line flags.
# Priority: CLI flags > Environment variables > Defaults
#
# Defaults are for the current opstool deployment in westus3.
# For other regions/environments, set the OPSTOOL_* environment variables.
#
KEYVAULT_NAME="${OPSTOOL_KEYVAULT_NAME:-opstool-kv-usw3}"
RESOURCE_GROUP="${OPSTOOL_RESOURCE_GROUP:-opstool-westus3}"
AKS_CLUSTER_NAME="${OPSTOOL_AKS_CLUSTER:-opstool-wus3}"
NAMESPACE="${OPSTOOL_NAMESPACE:-tenant-quota}"
DEPLOYMENT_NAME="${OPSTOOL_DEPLOYMENT:-tenant-quota-collector}"

EXPIRY_YEARS=1
RESTART_POD=false
SKIP_CONFIRM=false
TENANT_NAME=""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

ensure_kubectl_access() {
    print_info "Checking kubectl access to cluster..."
    
    if kubectl get namespace "$NAMESPACE" &>/dev/null; then
        print_success "kubectl can reach the cluster"
        return 0
    fi
    
    print_warning "Cannot reach cluster. Fetching AKS credentials..."
    
    if ! az aks get-credentials \
        --resource-group "$RESOURCE_GROUP" \
        --name "$AKS_CLUSTER_NAME" \
        --overwrite-existing 2>/dev/null; then
        print_error "Failed to get AKS credentials. Make sure you're logged in with:"
        echo "  az login"
        echo ""
        echo "Then try again, or manually get credentials with:"
        echo "  az aks get-credentials --resource-group $RESOURCE_GROUP --name $AKS_CLUSTER_NAME"
        return 1
    fi
    
    print_success "AKS credentials fetched successfully"
    return 0
}

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Renews the client secret for a tenant-quota service principal."
    echo ""
    echo "Options:"
    echo "  --tenant NAME      Tenant name (e.g., RedHat0)"
    echo "  --expiry YEARS     Secret validity in years (default: 1)"
    echo "  --restart          Restart the collector pod after update"
    echo "  --yes, -y          Skip confirmation prompt (for automation)"
    echo "  --list             List configured tenants and their SP expiration"
    echo "  --help             Show this help message"
    echo ""
    echo "Infrastructure Overrides (or use environment variables):"
    echo "  --keyvault NAME    Key Vault name (env: OPSTOOL_KEYVAULT_NAME)"
    echo "  --resource-group   Resource group (env: OPSTOOL_RESOURCE_GROUP)"
    echo "  --cluster NAME     AKS cluster name (env: OPSTOOL_AKS_CLUSTER)"
    echo "  --namespace NAME   Kubernetes namespace (env: OPSTOOL_NAMESPACE)"
    echo ""
    echo "Current Configuration:"
    echo "  Key Vault:      $KEYVAULT_NAME"
    echo "  Resource Group: $RESOURCE_GROUP"
    echo "  AKS Cluster:    $AKS_CLUSTER_NAME"
    echo "  Namespace:      $NAMESPACE"
    echo "  Deployment:     $DEPLOYMENT_NAME"
    echo ""
    echo "Prerequisites:"
    echo "  1. Azure CLI logged into the correct tenant (for SP renewal):"
    echo "     az login --tenant <azure-ad-tenant-id>"
    echo ""
    echo "  2. For --restart: kubectl access to opstool cluster (auto-fetched if needed)"
    echo ""
    echo "Examples:"
    echo "  $0 --list                           # Show all tenants and secret expiration"
    echo "  $0 --tenant RedHat0                 # Renew RedHat0 SP secret (1 year)"
    echo "  $0 --tenant RedHat0 --expiry 2      # Renew with 2-year expiry"
    echo "  $0 --tenant RedHat0 --restart       # Renew and restart pod"
    echo "  $0 --tenant RedHat0 --yes           # Renew without confirmation (automation)"
    echo ""
    echo "Environment Variable Override Example:"
    echo "  OPSTOOL_KEYVAULT_NAME=my-kv OPSTOOL_RESOURCE_GROUP=my-rg $0 --list"
    echo ""
    echo "Adding New Tenants:"
    echo "  To add a new tenant, update the TENANTS array in this script with:"
    echo "    \"DisplayName:AzureADTenantId:ClientId:KeyVaultSecretName\""
    echo ""
    echo "  Also update deploy/values.yaml with the same tenant configuration."
}

# =============================================================================
# TENANT CONFIGURATIONS
# =============================================================================
# Format: "DisplayName:AzureADTenantId:ServicePrincipalClientId:KeyVaultSecretName"
#
# MULTI-TENANT SUPPORT:
# This collector monitors Azure AD directory quota (users, groups, apps, etc.)
# which is per-tenant, NOT per-subscription. Each Azure AD tenant has one quota.
#
# - Each entry represents ONE service principal for ONE Azure AD tenant
# - To monitor multiple tenants, add an entry for each with its own SP
# - The DisplayName is just for identification (used in --tenant flag)
# - The AzureADTenantId is used to warn you which tenant to login to
#
# To add a new tenant/SP:
# 1. Create the SP in the target tenant (see deploy/values.yaml for full instructions)
# 2. Store the secret in Key Vault
# 3. Add an entry here in the same format
# 4. Add the corresponding entry in deploy/values.yaml
#
# EXAMPLE - SPs from DIFFERENT Azure AD tenants:
#   "RedHat0:64dc69e4-...:abc123:custom-metrics-collector-redhat0-client-secret"
#   "Microsoft:72f988bf-...:xyz789:custom-metrics-collector-microsoft-client-secret"
#
# IMPORTANT: When renewing, you must be logged into the correct Azure AD tenant:
#   az login --tenant <tenant-id>
#
TENANTS=(
    "RedHat0:64dc69e4-d083-49fc-9569-ebece1dd1408:1ef710d1-afd7-4bf3-8095-e8126650607f:custom-metrics-collector-redhat0-client-secret"
    # Add more tenants as needed (each Azure AD tenant has its own directory quota):
    # "Microsoft:72f988bf-86f1-41af-91ab-2d7cd011db47:<client-id>:<secret-name>"
)

get_tenant_config() {
    local tenant_name="$1"
    for tenant in "${TENANTS[@]}"; do
        IFS=':' read -r name azure_tenant_id client_id secret_name <<< "$tenant"
        if [[ "$name" == "$tenant_name" ]]; then
            echo "$azure_tenant_id:$client_id:$secret_name"
            return 0
        fi
    done
    return 1
}

get_tenant_names() {
    for tenant in "${TENANTS[@]}"; do
        IFS=':' read -r name _ _ _ <<< "$tenant"
        echo -n "$name "
    done
}

list_tenants() {
    print_info "Configured tenants and their service principal secrets:"
    echo ""
    
    for tenant in "${TENANTS[@]}"; do
        IFS=':' read -r name azure_tenant_id sp_client_id kv_secret_name <<< "$tenant"
        
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        echo -e "${BLUE}Tenant Display Name:${NC} $name"
        echo -e "${BLUE}Azure AD Tenant ID:${NC} $azure_tenant_id"
        echo -e "${BLUE}Service Principal Client ID:${NC} $sp_client_id"
        echo -e "${BLUE}Key Vault Secret Name:${NC} $kv_secret_name"
        echo ""
        
        print_info "Fetching service principal details from Azure AD..."
        local sp_name
        sp_name=$(az ad sp show --id "$sp_client_id" --query "displayName" -o tsv 2>/dev/null)
        
        if [[ -n "$sp_name" ]]; then
            echo -e "${BLUE}Service Principal Name:${NC} $sp_name"
            echo ""
            
            echo -e "${BLUE}Client Secret Credentials (from Azure AD):${NC}"
            local creds
            creds=$(az ad app credential list --id "$sp_client_id" --query "[].{displayName:displayName,endDateTime:endDateTime,keyId:keyId,hint:hint}" -o table 2>/dev/null)
            
            if [[ -n "$creds" && "$creds" != *"[]"* ]]; then
                echo "$creds"
            else
                creds=$(az ad sp credential list --id "$sp_client_id" --query "[].{displayName:displayName,endDateTime:endDateTime,keyId:keyId}" -o table 2>/dev/null)
                
                if [[ -n "$creds" && "$creds" != *"[]"* ]]; then
                    echo "$creds"
                else
                    print_warning "No client secrets found for this SP"
                fi
            fi
        else
            echo ""
            print_error "Cannot fetch SP details - not logged into the correct tenant!"
            echo ""
            echo "  To see SP credentials and expiration dates, run:"
            echo -e "  ${GREEN}az login --tenant $azure_tenant_id${NC}"
            echo ""
            echo "  Then run this script again:"
            echo "  ./scripts/renew-sp-secret.sh --list"
        fi
        
        echo ""
        echo -e "${BLUE}Key Vault Secret Info:${NC}"
        az keyvault secret show --vault-name "$KEYVAULT_NAME" --name "$kv_secret_name" --query "{name:name,created:attributes.created,updated:attributes.updated,enabled:attributes.enabled}" -o table 2>/dev/null || print_warning "Could not access Key Vault secret. Run: az login"
        
        echo ""
    done
}

renew_secret() {
    local tenant="$1"
    local expiry_years="$2"
    
    local config
    if ! config=$(get_tenant_config "$tenant"); then
        print_error "Unknown tenant: $tenant"
        echo "Available tenants: $(get_tenant_names)"
        exit 1
    fi
    
    IFS=':' read -r azure_tenant_id sp_client_id kv_secret_name <<< "$config"
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    print_info "Renewing secret for: $tenant"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo -e "${BLUE}Azure AD Tenant ID:${NC} $azure_tenant_id"
    echo -e "${BLUE}Service Principal Client ID:${NC} $sp_client_id"
    echo -e "${BLUE}Key Vault Secret Name:${NC} $kv_secret_name"
    echo -e "${BLUE}New secret expiry:${NC} $expiry_years year(s)"
    echo ""
    
    print_warning "IMPORTANT: You must be logged into the correct Azure AD tenant!"
    echo ""
    echo "  To renew this service principal's secret, run:"
    echo -e "  ${GREEN}az login --tenant $azure_tenant_id${NC}"
    echo ""
    echo "  After renewal, the script will update Key Vault (in dev tenant)."
    echo ""
    
    if [[ "$SKIP_CONFIRM" != "true" ]]; then
        read -p "Are you logged into tenant $azure_tenant_id? Proceed? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "Cancelled. Please login to the correct tenant first:"
            echo "  az login --tenant $azure_tenant_id"
            exit 0
        fi
    else
        print_info "Skipping confirmation (--yes flag set)"
    fi
    
    local expiry_date
    if date -v+1d > /dev/null 2>&1; then
        # macOS
        expiry_date=$(date -v+${expiry_years}y +%Y-%m-%d)
    else
        # Linux
        expiry_date=$(date -d "+${expiry_years} years" +%Y-%m-%d)
    fi
    
    print_info "Creating new client secret with expiry: $expiry_date"
    
    local new_secret
    new_secret=$(az ad sp credential reset \
        --id "$sp_client_id" \
        --display-name "tenant-quota-collector-$(date +%Y%m%d)" \
        --end-date "$expiry_date" \
        --query password \
        -o tsv)
    
    if [[ -z "$new_secret" ]]; then
        print_error "Failed to create new client secret"
        exit 1
    fi
    
    print_success "New client secret created"
    print_info "Updating Key Vault secret: $kv_secret_name"
    
    az keyvault secret set \
        --vault-name "$KEYVAULT_NAME" \
        --name "$kv_secret_name" \
        --value "$new_secret" \
        --description "Renewed $(date +%Y-%m-%d), expires $expiry_date" \
        > /dev/null
    
    print_success "Key Vault secret updated"
    
    echo ""
    print_info "New credential details:"
    az ad sp credential list --id "$sp_client_id" --query "[?contains(displayName, '$(date +%Y%m%d)')].{keyId:keyId,displayName:displayName,endDateTime:endDateTime}" -o table 2>/dev/null || true
    
    if [[ "$RESTART_POD" == "true" ]]; then
        echo ""
        if ! ensure_kubectl_access; then
            print_error "Cannot restart pod - kubectl access failed"
            print_info "You can manually restart later with:"
            echo "  az aks get-credentials --resource-group $RESOURCE_GROUP --name $AKS_CLUSTER_NAME"
            echo "  kubectl rollout restart deployment/$DEPLOYMENT_NAME -n $NAMESPACE"
        else
            print_info "Restarting collector pod..."
            kubectl rollout restart deployment/$DEPLOYMENT_NAME -n $NAMESPACE
            print_success "Pod restart initiated"
            
            print_info "Waiting for rollout to complete..."
            kubectl rollout status deployment/$DEPLOYMENT_NAME -n $NAMESPACE --timeout=120s
            print_success "Rollout complete"
        fi
    else
        echo ""
        print_warning "Pod not restarted. The CSI driver will pick up the new secret within ~2 minutes."
        print_info "To restart immediately, run:"
        echo "  az aks get-credentials --resource-group $RESOURCE_GROUP --name $AKS_CLUSTER_NAME"
        echo "  kubectl rollout restart deployment/$DEPLOYMENT_NAME -n $NAMESPACE"
    fi
    
    echo ""
    print_success "Secret renewal complete!"
    echo ""
    print_warning "IMPORTANT: Old client secrets are still valid until they expire."
    print_info "To delete old secrets (optional), use:"
    echo "  az ad sp credential delete --id $sp_client_id --key-id <old-key-id>"
    echo ""
    print_info "List current secrets with:"
    echo "  az ad sp credential list --id $sp_client_id -o table"
}

while [[ $# -gt 0 ]]; do
    case $1 in
        --tenant)
            TENANT_NAME="$2"
            shift 2
            ;;
        --expiry)
            EXPIRY_YEARS="$2"
            shift 2
            ;;
        --restart)
            RESTART_POD=true
            shift
            ;;
        --yes|-y)
            SKIP_CONFIRM=true
            shift
            ;;
        --keyvault)
            KEYVAULT_NAME="$2"
            shift 2
            ;;
        --resource-group)
            RESOURCE_GROUP="$2"
            shift 2
            ;;
        --cluster)
            AKS_CLUSTER_NAME="$2"
            shift 2
            ;;
        --namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --list)
            list_tenants
            exit 0
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

if [[ -z "$TENANT_NAME" ]]; then
    list_tenants
    echo ""
    echo "Available tenants: $(get_tenant_names)"
    read -p "Enter tenant name to renew (or Ctrl+C to cancel): " TENANT_NAME
fi

renew_secret "$TENANT_NAME" "$EXPIRY_YEARS"
