#!/bin/bash

# Test script to verify mock FPA restriction policies are working correctly
# This script tests that the mock FPA has minimum required permissions and dangerous operations are blocked

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Get script directory for relative paths
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source environment variables from dev-application.sh
if ! eval "$($SCRIPT_DIR/dev-application.sh shell)"; then
    echo -e "${RED}Error: Could not source environment from dev-application.sh${NC}"
    echo "Make sure the dev applications are created first by running:"
    echo "  $SCRIPT_DIR/dev-application.sh create"
    exit 1
fi

# Map exported variables to expected names
SUBSCRIPTION_ID="${SUBSCRIPTION_ID:-}"
RESOURCE_GROUP="${RESOURCE_GROUP:-}"
KEY_VAULT_NAME="${ARO_HCP_DEV_KEY_VAULT_NAME:-}"
FP_APPLICATION_NAME="${ARO_HCP_DEV_FP_APPLICATION_NAME:-}"
FP_CERTIFICATE_NAME="${ARO_HCP_DEV_FP_CERTIFICATE_NAME:-}"
AH_APPLICATION_NAME="${ARO_HCP_DEV_AH_APPLICATION_NAME:-}"

# Validate required variables are set
validate_environment() {
    local missing_vars=()
    
    [[ -z "$SUBSCRIPTION_ID" ]] && missing_vars+=("SUBSCRIPTION_ID")
    [[ -z "$RESOURCE_GROUP" ]] && missing_vars+=("RESOURCE_GROUP") 
    [[ -z "$KEY_VAULT_NAME" ]] && missing_vars+=("KEY_VAULT_NAME")
    [[ -z "$FP_APPLICATION_NAME" ]] && missing_vars+=("FP_APPLICATION_NAME")
    [[ -z "$FP_CERTIFICATE_NAME" ]] && missing_vars+=("FP_CERTIFICATE_NAME")
    
    if [[ ${#missing_vars[@]} -gt 0 ]]; then
        echo -e "${RED}Error: Missing required environment variables: ${missing_vars[*]}${NC}"
        echo "Make sure the dev applications are created first by running:"
        echo "  $SCRIPT_DIR/dev-application.sh create"
        exit 1
    fi
    
    print_info "Environment validated successfully"
}

# Test results tracking
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_TOTAL=0

print_header() {
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}"
}

print_test() {
    echo -e "${YELLOW}Testing: $1${NC}"
}

print_success() {
    echo -e "${GREEN}✅ PASS: $1${NC}"
    ((TESTS_PASSED++))
    ((TESTS_TOTAL++))
}

print_failure() {
    echo -e "${RED}❌ FAIL: $1${NC}"
    ((TESTS_FAILED++))
    ((TESTS_TOTAL++))
}

print_info() {
    echo -e "${BLUE}ℹ️ INFO: $1${NC}"
}

# Test if an operation should succeed
test_should_succeed() {
    local test_name="$1"
    local command="$2"
    
    print_test "$test_name (should succeed)"
    
    if eval "$command" >/dev/null 2>&1; then
        print_success "$test_name"
        return 0
    else
        print_failure "$test_name - Operation was blocked but should have succeeded"
        return 1
    fi
}

# Test if an operation should be blocked
test_should_fail() {
    local test_name="$1"
    local command="$2"
    
    print_test "$test_name (should be blocked)"
    
    if eval "$command" >/dev/null 2>&1; then
        print_failure "$test_name - Operation succeeded but should have been blocked"
        return 1
    else
        print_success "$test_name - Correctly blocked"
        return 0
    fi
}

# Save current user context
save_current_user() {
    ORIGINAL_USER=$(az account show --query user.name -o tsv 2>/dev/null || echo "unknown")
    ORIGINAL_USER_TYPE=$(az account show --query user.type -o tsv 2>/dev/null || echo "unknown")
    print_info "Original user: $ORIGINAL_USER ($ORIGINAL_USER_TYPE)"
}

# Restore original user context
restore_original_user() {
    print_info "Restoring original user context..."
    if [[ "$ORIGINAL_USER_TYPE" == "user" ]]; then
        az login >/dev/null 2>&1 || {
            echo -e "${YELLOW}⚠️  Please run 'az login' to restore your session${NC}"
        }
    else
        echo -e "${YELLOW}⚠️  Please restore your original authentication context${NC}"
    fi
}

# Get a test resource group for non-destructive tests
get_test_resource_group() {
    # Try to find an existing resource group, or use the dev application RG
    TEST_RG=$(az group list --query "[0].name" -o tsv 2>/dev/null || echo "$RESOURCE_GROUP")
    if [[ -z "$TEST_RG" ]]; then
        print_failure "No resource groups found for testing"
        exit 1
    fi
    print_info "Using test resource group: $TEST_RG"
}

# Test basic read operations (should work with Contributor role)
test_read_operations() {
    print_header "Testing Read Operations (Should Succeed)"
    
    test_should_succeed "List resource groups" \
        "az group list --query '[].name' -o tsv"
    
    test_should_succeed "List storage accounts" \
        "az storage account list --query '[].name' -o tsv"
    
    test_should_succeed "List virtual networks" \
        "az network vnet list --query '[].name' -o tsv"
    
    test_should_succeed "List key vaults" \
        "az keyvault list --query '[].name' -o tsv"
    
    test_should_succeed "Show subscription details" \
        "az account show --query id -o tsv"
}

# Test check access operations (requires built-in role)
test_check_access_operations() {
    print_header "Testing Check Access Operations (Should Succeed)"
    
    # Get current user's object ID for check access tests
    local current_user_id=$(az ad signed-in-user show --query id -o tsv 2>/dev/null || echo "")
    
    if [[ -n "$current_user_id" ]]; then
        test_should_succeed "Check access for current user" \
            "az role assignment list --assignee '$current_user_id' --query '[].roleDefinitionName' -o tsv"
    else
        print_info "Skipping check access test - cannot get current user ID"
    fi
    
    test_should_succeed "List role definitions" \
        "az role definition list --query '[0].roleName' -o tsv"
}

# Test policy-restricted operations (should be blocked)
test_restricted_operations() {
    print_header "Testing Restricted Operations (Should Be Blocked)"
    
    # Test role assignment creation (should be blocked)
    test_should_fail "Create role assignment" \
        "az role assignment create --assignee '$FP_APPLICATION_NAME' --role 'Reader' --scope '/subscriptions/$SUBSCRIPTION_ID' --dry-run"
    
    # Test role definition creation (should be blocked)
    test_should_fail "Create custom role definition" \
        "az role definition create --role-definition '{\"Name\":\"TestRole-$USER\",\"Description\":\"Test\",\"Actions\":[\"Microsoft.Storage/*/read\"],\"AssignableScopes\":[\"/subscriptions/$SUBSCRIPTION_ID\"]}'"
    
    # Test policy assignment creation (should be blocked)
    test_should_fail "Create policy assignment" \
        "az policy assignment create --name 'test-policy-$USER' --policy '/providers/Microsoft.Authorization/policyDefinitions/56a914f7-8874-476c-8bbc-d748663e4d06' --scope '/subscriptions/$SUBSCRIPTION_ID'"
    
    # Test critical resource deletion (should be blocked)
    test_should_fail "Delete resource group (dry run)" \
        "az group delete --name '$TEST_RG' --yes --dry-run"
    
    test_should_fail "Delete key vault (dry run)" \
        "az keyvault delete --name '$KEY_VAULT_NAME' --dry-run"
}

# Test VM creation (should be blocked by policy)
test_vm_operations() {
    print_header "Testing VM Operations (Should Be Blocked)"
    
    # Test VM creation (should be blocked)
    test_should_fail "Create virtual machine" \
        "az vm create --resource-group '$TEST_RG' --name 'test-vm-$USER' --image 'UbuntuLTS' --admin-username 'testuser' --generate-ssh-keys --dry-run"
}

# Test network operations (service association links should be allowed)
test_network_operations() {
    print_header "Testing Network Operations"
    
    # Find a VNet/subnet for testing
    local test_vnet=$(az network vnet list --resource-group "$TEST_RG" --query "[0].name" -o tsv 2>/dev/null || echo "")
    local test_subnet=$(az network vnet subnet list --resource-group "$TEST_RG" --vnet-name "$test_vnet" --query "[0].name" -o tsv 2>/dev/null || echo "")
    
    if [[ -n "$test_vnet" && -n "$test_subnet" ]]; then
        print_info "Testing with VNet: $test_vnet, Subnet: $test_subnet"
        
        # Reading subnet should work
        test_should_succeed "Read subnet configuration" \
            "az network vnet subnet show --resource-group '$TEST_RG' --vnet-name '$test_vnet' --name '$test_subnet' --query 'name' -o tsv"
        
        # Service association links should be allowed (but we'll just test read access)
        test_should_succeed "List service association links" \
            "az network vnet subnet show --resource-group '$TEST_RG' --vnet-name '$test_vnet' --name '$test_subnet' --query 'serviceAssociationLinks' -o tsv"
    else
        print_info "No VNet/subnet found for network operations testing"
    fi
}

# Main test execution
main() {
    print_header "Mock FPA Policy Restriction Tests"
    
    # Validate environment variables
    validate_environment
    
    print_info "Testing environment: $SUBSCRIPTION_ID"
    print_info "Mock FPA Application: $FP_APPLICATION_NAME"
    
    # Save current context
    save_current_user
    
    # Get test resources
    get_test_resource_group
    
    # Login as mock FPA
    print_info "Switching to mock FPA identity..."
    if ! $SCRIPT_DIR/dev-application.sh login >/dev/null 2>&1; then
        print_failure "Failed to login as mock FPA"
        exit 1
    fi
    
    # Verify we're logged in as the service principal
    local current_user=$(az account show --query user.name -o tsv)
    local current_type=$(az account show --query user.type -o tsv)
    
    if [[ "$current_type" != "servicePrincipal" ]]; then
        print_failure "Not logged in as service principal (type: $current_type)"
        restore_original_user
        exit 1
    fi
    
    print_info "Successfully logged in as: $current_user ($current_type)"
    
    # Run test suites
    test_read_operations
    test_check_access_operations
    test_restricted_operations
    test_vm_operations
    test_network_operations
    
    # Restore original context
    restore_original_user
    
    # Print summary
    print_header "Test Results Summary"
    echo -e "${GREEN}Tests Passed: $TESTS_PASSED${NC}"
    echo -e "${RED}Tests Failed: $TESTS_FAILED${NC}"
    echo -e "${BLUE}Total Tests: $TESTS_TOTAL${NC}"
    
    if [[ $TESTS_FAILED -eq 0 ]]; then
        echo -e "${GREEN}🎉 All tests passed! Mock FPA policies are working correctly.${NC}"
        exit 0
    else
        echo -e "${RED}❌ Some tests failed. Please review the policy configuration.${NC}"
        exit 1
    fi
}

# Handle script interruption
trap 'restore_original_user; exit 1' INT TERM

# Check if required tools are available
if ! command -v az >/dev/null 2>&1; then
    echo -e "${RED}Error: Azure CLI (az) is not installed or not in PATH${NC}"
    exit 1
fi



# Run main function
main "$@"
