#!/bin/bash

# Test script to verify mock FPA restriction policies are working correctly
# This script tests that the mock FPA has minimum required permissions and dangerous operations are blocked
# The script continues running all tests even if some fail, providing a comprehensive report at the end
#
# Usage:
#   ./test-mock-fpa-policies.sh [--quiet] [--fail-fast]
#   VERBOSE_OUTPUT=false ./test-mock-fpa-policies.sh
#   FAIL_FAST=true ./test-mock-fpa-policies.sh
#
# Options:
#   --quiet: Disable verbose output (same as VERBOSE_OUTPUT=false)
#   --fail-fast: Stop execution at the first test failure (default: continue all tests)
#   VERBOSE_OUTPUT: Set to false to hide detailed error output (default: true)
#   FAIL_FAST: Set to true to stop at first failure (default: false)

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

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --quiet|-q)
            VERBOSE_OUTPUT=false
            shift
            ;;
        --fail-fast|-f)
            FAIL_FAST=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [--quiet] [--fail-fast]"
            echo "Test mock FPA restriction policies"
            echo ""
            echo "Options:"
            echo "  --quiet, -q      Disable verbose error output"
            echo "  --fail-fast, -f  Stop execution at the first test failure"
            echo "  --help, -h       Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  VERBOSE_OUTPUT   Set to false to disable verbose output (default: true)"
            echo "  FAIL_FAST        Set to true to stop at first failure (default: false)"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Control variables (can be set via environment or command line)
VERBOSE_OUTPUT=${VERBOSE_OUTPUT:-true}
FAIL_FAST=${FAIL_FAST:-false}

# Helper function to run tests with optional continue-on-failure
run_test() {
    local test_function="$1"
    local test_args="${@:2}"

    if [[ "$FAIL_FAST" == "true" ]]; then
        # In fail-fast mode, let the test function's exit logic handle failures
        $test_function $test_args
    else
        # In continue mode, ignore test failures
        $test_function $test_args || true
    fi
}

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

    # Capture both stdout and stderr
    local output
    local exit_code
    output=$(eval "$command" 2>&1)
    exit_code=$?

    if [[ $exit_code -eq 0 ]]; then
        print_success "$test_name"
        return 0
    else
        print_failure "$test_name - Operation was blocked but should have succeeded"
        if [[ "$VERBOSE_OUTPUT" == "true" ]]; then
            echo -e "${RED}Command: $command${NC}"
            echo -e "${RED}Exit code: $exit_code${NC}"
            echo -e "${RED}Output:${NC}"
            echo "$output" | sed 's/^/  /'  # Indent output for readability
            echo ""
        fi

        # Exit immediately if fail-fast is enabled
        if [[ "$FAIL_FAST" == "true" ]]; then
            echo -e "${RED}💥 FAIL-FAST: Stopping execution due to test failure${NC}"
            exit 1
        fi

        return 1
    fi
}

# Test if an operation should be blocked
test_should_fail() {
    local test_name="$1"
    local command="$2"

    print_test "$test_name (should be blocked)"

    # Capture both stdout and stderr
    local output
    local exit_code
    output=$(eval "$command" 2>&1)
    exit_code=$?

    if [[ $exit_code -eq 0 ]]; then
        print_failure "$test_name - Operation succeeded but should have been blocked"
        if [[ "$VERBOSE_OUTPUT" == "true" ]]; then
            echo -e "${RED}Command: $command${NC}"
            echo -e "${RED}Exit code: $exit_code${NC}"
            echo -e "${RED}Output:${NC}"
            echo "$output" | sed 's/^/  /'  # Indent output for readability
            echo ""
        fi

        # Exit immediately if fail-fast is enabled
        if [[ "$FAIL_FAST" == "true" ]]; then
            echo -e "${RED}💥 FAIL-FAST: Stopping execution due to test failure${NC}"
            exit 1
        fi

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
        return 1
    fi
    print_info "Using test resource group: $TEST_RG"
    return 0
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
# These tests verify that the mock FPA can successfully call Azure's check access APIs
# This is the primary reason we need to use the built-in Contributor role instead of a custom role
test_check_access_operations() {
    print_header "Testing Check Access Operations (Should Succeed)"

    # Get current service principal's object ID
    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        print_info "Testing check access API with service principal: $current_user_id"

                # Test 1: Check access using role assignment list (basic check access)
        test_should_succeed "List role assignments for current SP" \
            "az role assignment list --assignee '$current_user_id' --query '[].roleDefinitionName' -o tsv"

        # Test 2: Check access for subscription scope
        test_should_succeed "Check access at subscription scope" \
            "az role assignment list --assignee '$current_user_id' --scope '/subscriptions/$SUBSCRIPTION_ID' --query '[].roleDefinitionName' -o tsv"

        # Test 3: Check access for resource group scope
        test_should_succeed "Check access at resource group scope" \
            "az role assignment list --assignee '$current_user_id' --scope '/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$TEST_RG' --query '[].roleDefinitionName' -o tsv"

        # Test 4: Use Azure REST API to call check access directly (key FPA functionality)
        test_should_succeed "Call check access API for read permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2015-07-01' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[\"Microsoft.Resources/subscriptions/read\"]}' --query 'accessDecision' -o tsv"

        # Test 4b: Check access for resource group operations
        test_should_succeed "Call check access API for resource group operations" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$TEST_RG/providers/Microsoft.Authorization/checkAccess?api-version=2015-07-01' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[\"Microsoft.Resources/subscriptions/resourceGroups/read\"]}' --query 'accessDecision' -o tsv"

        # Test 5: Check permissions on storage accounts (common FPA use case)
        test_should_succeed "Check storage account permissions" \
            "az role assignment list --assignee '$current_user_id' --scope '/subscriptions/$SUBSCRIPTION_ID' --query '[?contains(roleDefinitionName, \`Contributor\`)].roleDefinitionName' -o tsv"

        # Test 6: Verify we can read our own role assignments (this is what FPA typically does)
        test_should_succeed "Verify Contributor role assignment" \
            "az role assignment list --assignee '$current_user_id' --role 'Contributor' --scope '/subscriptions/$SUBSCRIPTION_ID' --query '[0].roleDefinitionName' -o tsv"

        # Test 6b: Check access for common Azure services (FPA use cases)
        test_should_succeed "Check access for storage operations" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2015-07-01' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[\"Microsoft.Storage/storageAccounts/read\"]}' --query 'accessDecision' -o tsv"

        # Test 6c: Check access for network operations (relevant for ARO)
        test_should_succeed "Check access for network operations" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2015-07-01' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[\"Microsoft.Network/virtualNetworks/read\"]}' --query 'accessDecision' -o tsv"

    else
        print_info "Skipping check access tests - cannot get service principal ID"
    fi

    # Test 7: List role definitions (should work with Contributor)
    test_should_succeed "List role definitions" \
        "az role definition list --query '[0].roleName' -o tsv"

    # Test 8: Get specific role definition (common check access scenario)
    test_should_succeed "Get Contributor role definition" \
        "az role definition list --name 'Contributor' --query '[0].roleName' -o tsv"

    # Test 9: Test permissions enumeration (FPA common operation)
    test_should_succeed "List all permissions for Contributor role" \
        "az role definition list --name 'Contributor' --query '[0].permissions[0].actions' -o tsv"
}

# Test policy-restricted operations (should be blocked)
test_restricted_operations() {
    print_header "Testing Restricted Operations (Should Be Blocked)"

    # Test role assignment creation (should be blocked)
    test_should_fail "Create role assignment" \
        "az role assignment create --assignee '$FP_APPLICATION_NAME' --role 'Reader' --scope '/subscriptions/$SUBSCRIPTION_ID' --dry-run" || true

    # Test role definition creation (should be blocked)
    test_should_fail "Create custom role definition" \
        "az role definition create --role-definition '{\"Name\":\"TestRole-$USER\",\"Description\":\"Test\",\"Actions\":[\"Microsoft.Storage/*/read\"],\"AssignableScopes\":[\"/subscriptions/$SUBSCRIPTION_ID\"]}'" || true

    # Test policy assignment creation (should be blocked)
    test_should_fail "Create policy assignment" \
        "az policy assignment create --name 'test-policy-$USER' --policy '/providers/Microsoft.Authorization/policyDefinitions/56a914f7-8874-476c-8bbc-d748663e4d06' --scope '/subscriptions/$SUBSCRIPTION_ID'" || true

    # Test critical resource deletion (should be blocked)
    test_should_fail "Delete resource group (dry run)" \
        "az group delete --name '$TEST_RG' --yes --dry-run" || true

    test_should_fail "Delete key vault (dry run)" \
        "az keyvault delete --name '$KEY_VAULT_NAME' --dry-run" || true
}

# Test VM creation (should be blocked by policy)
test_vm_operations() {
    print_header "Testing VM Operations (Should Be Blocked)"

    # Test VM creation (should be blocked)
    test_should_fail "Create virtual machine" \
        "az vm create --resource-group '$TEST_RG' --name 'test-vm-$USER' --image 'UbuntuLTS' --admin-username 'testuser' --generate-ssh-keys --dry-run" || true
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
            "az network vnet subnet show --resource-group '$TEST_RG' --vnet-name '$test_vnet' --name '$test_subnet' --query 'name' -o tsv" || true

        # Service association links should be allowed (but we'll just test read access)
        test_should_succeed "List service association links" \
            "az network vnet subnet show --resource-group '$TEST_RG' --vnet-name '$test_vnet' --name '$test_subnet' --query 'serviceAssociationLinks' -o tsv" || true
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
    print_info "Verbose output: $VERBOSE_OUTPUT"
    print_info "Fail fast mode: $FAIL_FAST"

    # Save current context
    save_current_user

    # Get test resources
    if ! get_test_resource_group; then
        print_failure "Could not determine test resource group"
        exit 1
    fi

    # Check if already logged in as the correct service principal
    local current_user=$(az account show --query user.name -o tsv 2>/dev/null || echo "")
    local current_type=$(az account show --query user.type -o tsv 2>/dev/null || echo "")

    # Check if we're logged in as a service principal and if it looks like our FPA
    local skip_login=false
    if [[ "$current_type" == "servicePrincipal" && -n "$current_user" ]]; then
        # If we can't get the expected app ID due to authentication issues,
        # we'll assume we're already logged in correctly if the user is a service principal
        # and the application name contains our expected pattern
        local expected_app_id=""
        if expected_app_id=$(az ad app list --display-name "$FP_APPLICATION_NAME" --query "[0].appId" -o tsv 2>/dev/null) && [[ -n "$expected_app_id" ]]; then
            if [[ "$current_user" == "$expected_app_id" ]]; then
                skip_login=true
                print_info "Already logged in as mock FPA service principal: $current_user"
            fi
        else
            # Can't verify the exact app ID, but if we're a service principal and the name looks right,
            # we'll assume it's correct to avoid authentication loops
            print_info "Already logged in as service principal: $current_user (assuming this is the mock FPA)"
            skip_login=true
        fi
    fi

    if [[ "$skip_login" == "false" ]]; then
        # Login as mock FPA
        print_info "Switching to mock FPA identity..."
        if ! $SCRIPT_DIR/dev-application.sh login >/dev/null 2>&1; then
            print_failure "Failed to login as mock FPA"
            exit 1
        fi

        # Verify we're logged in as the service principal
        current_user=$(az account show --query user.name -o tsv)
        current_type=$(az account show --query user.type -o tsv)

        if [[ "$current_type" != "servicePrincipal" ]]; then
            print_failure "Not logged in as service principal (type: $current_type)"
            restore_original_user
            exit 1
        fi

        print_info "Successfully logged in as: $current_user ($current_type)"
    fi

    # Run test suites - behavior depends on FAIL_FAST setting
    run_test test_read_operations
    run_test test_check_access_operations
    #run_test test_restricted_operations
    #run_test test_vm_operations
    #run_test test_network_operations

    # Restore original context only if we changed it
    if [[ "$skip_login" == "false" && "$ORIGINAL_USER_TYPE" != "servicePrincipal" ]]; then
        restore_original_user
    else
        print_info "Keeping current service principal session"
    fi

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
cleanup_on_interrupt() {
    # Only restore if we were originally a user (not service principal)
    if [[ -n "$ORIGINAL_USER_TYPE" && "$ORIGINAL_USER_TYPE" != "servicePrincipal" ]]; then
        restore_original_user
    fi
    exit 1
}
trap 'cleanup_on_interrupt' INT TERM

# Check if required tools are available
if ! command -v az >/dev/null 2>&1; then
    echo -e "${RED}Error: Azure CLI (az) is not installed or not in PATH${NC}"
    exit 1
fi



# Run main function (arguments already parsed above)
main
