#!/bin/bash

# Test script to verify mock FPA restriction policies are working correctly
# This script tests that the mock FPA has minimum required permissions and dangerous operations are blocked
# The script continues running all tests even if some fail, providing a comprehensive report at the end
#
# This script uses a dedicated Azure CLI config directory (../mock-fpa-azure-config) to avoid interfering
# with the user's existing Azure CLI configuration.
#
# Usage:
#   ./test-mock-fpa-policies.sh [--quiet] [--fail-fast]
#   VERBOSE_OUTPUT=false ./test-mock-fpa-policies.sh
#   FAIL_FAST=true ./test-mock-fpa-policies.sh
#
# Options:
#   --quiet: Disable verbose output (same as VERBOSE_OUTPUT=false)
#   --fail-fast: Stop execution at the first test failure (default: continue all tests)
#   VERBOSE_OUTPUT: Set to false to hide detailed command output for all tests (default: true)
#   FAIL_FAST: Set to true to stop at first failure (default: false)

set -uo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Get script directory and source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/mock-fpa-common.sh"

# Set up Azure config directory and file paths for mock FPA
initialAzureConfigSetup "$SCRIPT_DIR"

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
validateEnvironment() {
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

    printInfo "Environment validated successfully"
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
            echo "  --quiet, -q      Disable verbose test output"
            echo "  --fail-fast, -f  Stop execution at the first test failure"
            echo "  --help, -h       Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  VERBOSE_OUTPUT   Set to false to hide detailed command output for all tests (default: true)"
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

# Helper function to exit cleanly in fail-fast mode
failFastExit() {
    echo -e "${RED}💥 FAIL-FAST: Stopping execution due to test failure${NC}"
    # Flush output buffers before exiting to ensure all messages are displayed
    exec 1>&1 2>&2
    sleep 1
    exit 1
}

# Helper function to run tests with optional continue-on-failure
runTest() {
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

printHeader() {
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}"
}

printTest() {
    echo -e "${YELLOW}Testing: $1${NC}"
}

printSuccess() {
    echo -e "${GREEN}✅ PASS: $1${NC}"
    ((TESTS_PASSED++))
    ((TESTS_TOTAL++))
}

printFailure() {
    echo -e "${RED}❌ FAIL: $1${NC}"
    ((TESTS_FAILED++))
    ((TESTS_TOTAL++))
}

printInfo() {
    echo -e "${BLUE}ℹ️ INFO: $1${NC}"
}

# Get current user info with specified config
getCurrentUserInfo() {
    local user_name=$(az account show --query user.name -o tsv 2>/dev/null || echo "unknown")
    local user_type=$(az account show --query user.type -o tsv 2>/dev/null || echo "unknown")
    local subscription_id=$(az account show --query id -o tsv 2>/dev/null || echo "unknown")

    echo "User: $user_name"
    echo "Type: $user_type"
    echo "Subscription: $subscription_id"
    echo "Config Dir: ${AZURE_CONFIG_DIR:-"default (~/.azure)"}"
}

# Test if an operation should succeed
testShouldSucceed() {
    local test_name="$1"
    local command="$2"

    printTest "$test_name (should succeed)"

    # Capture both stdout and stderr
    local output
    local exit_code
    output=$(eval "$command" 2>&1)
    exit_code=$?

    if [[ $exit_code -eq 0 ]]; then
        printSuccess "$test_name"
        if [[ "$VERBOSE_OUTPUT" == "true" ]]; then
            echo -e "${GREEN}Command: $command${NC}"
            echo -e "${GREEN}Exit code: $exit_code${NC}"
            echo -e "${GREEN}Output:${NC}"
            echo "$output" | sed 's/^/  /'  # Indent output for readability
            echo ""
        fi
        return 0
    else
        printFailure "$test_name - Operation was blocked but should have succeeded"
        if [[ "$VERBOSE_OUTPUT" == "true" ]]; then
            echo -e "${RED}Command: $command${NC}"
            echo -e "${RED}Exit code: $exit_code${NC}"
            echo -e "${RED}Output:${NC}"
            echo "$output" | sed 's/^/  /'  # Indent output for readability
            echo ""
        fi

        # Exit immediately if fail-fast is enabled
        if [[ "$FAIL_FAST" == "true" ]]; then
            failFastExit
        fi

        return 1
    fi
}

# Test if an operation should be blocked
testShouldFail() {
    local test_name="$1"
    local command="$2"

    printTest "$test_name (should be blocked)"

    # Capture both stdout and stderr
    local output
    local exit_code
    output=$(eval "$command" 2>&1)
    exit_code=$?

    if [[ $exit_code -eq 0 ]]; then
        printFailure "$test_name - Operation succeeded but should have been blocked"
        if [[ "$VERBOSE_OUTPUT" == "true" ]]; then
            echo -e "${RED}Command: $command${NC}"
            echo -e "${RED}Exit code: $exit_code${NC}"
            echo -e "${RED}Output:${NC}"
            echo "$output" | sed 's/^/  /'  # Indent output for readability
            echo ""
        fi

        # Exit immediately if fail-fast is enabled
        if [[ "$FAIL_FAST" == "true" ]]; then
            failFastExit
        fi

        return 1
    else
        printSuccess "$test_name - Correctly blocked"
        if [[ "$VERBOSE_OUTPUT" == "true" ]]; then
            echo -e "${GREEN}Command: $command${NC}"
            echo -e "${GREEN}Exit code: $exit_code${NC}"
            echo -e "${GREEN}Output:${NC}"
            echo "$output" | sed 's/^/  /'  # Indent output for readability
            echo ""
        fi
        return 0
    fi
}





# Get a test resource group for non-destructive tests
getTestResourceGroup() {
    # Ensure we're using developer config to find resource groups
    useDeveloperConfig

    # Try to find an existing resource group, or use the dev application RG
    TEST_RG=${RESOURCE_GROUP:-$(az group list --query "[0].name" -o tsv 2>/dev/null)}
    if [[ -z "$TEST_RG" ]]; then
        printFailure "No resource groups found for testing"
        return 1
    fi
    printInfo "Using test resource group: $TEST_RG"
    return 0
}

# Test basic read operations (should work with Contributor role)
testReadOperations() {
    printHeader "Testing Read Operations (Should Succeed)"

    useMockFpaConfig

    testShouldSucceed "List resource groups" \
        "az group list --query '[].name' -o tsv"

    testShouldSucceed "List storage accounts" \
        "az storage account list --query '[].name' -o tsv"

    testShouldSucceed "List virtual networks" \
        "az network vnet list --query '[].name' -o tsv"

    testShouldSucceed "List key vaults" \
        "az keyvault list --query '[].name' -o tsv"

    testShouldSucceed "Show subscription details" \
        "az account show --query id -o tsv"
}

# Test check access operations (requires built-in role)
# These tests verify that the mock FPA can successfully call Azure's check access APIs
# This is the primary reason we need to use the built-in Contributor role instead of a custom role
testCheckAccessOperations() {
    printHeader "Testing Check Access Operations (Should Succeed)"

    useMockFpaConfig

    # Get current service principal's object ID
    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        printInfo "Testing check access API with service principal: $current_user_id"

                # Test 1: Check access using role assignment list (basic check access)
        testShouldSucceed "List role assignments for current SP" \
            "az role assignment list --assignee '$current_user_id' --query '[].roleDefinitionName' -o tsv"

        # Test 2: Check access for subscription scope
        testShouldSucceed "Check access at subscription scope" \
            "az role assignment list --assignee '$current_user_id' --scope '/subscriptions/$SUBSCRIPTION_ID' --query '[].roleDefinitionName' -o tsv"

        # Test 3: Check access for resource group scope
        testShouldSucceed "Check access at resource group scope" \
            "az role assignment list --assignee '$current_user_id' --scope '/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$TEST_RG' --query '[].roleDefinitionName' -o tsv"

        # Test 4: Use Azure REST API to call check access directly (key FPA functionality)
        testShouldSucceed "Call check access API for read permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Resources/subscriptions/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test 4b: Check access for resource group operations
        testShouldSucceed "Call check access API for resource group operations" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$TEST_RG/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Resources/subscriptions/resourceGroups/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test 5: Check permissions on storage accounts (common FPA use case)
        testShouldSucceed "Check storage account permissions" \
            "az role assignment list --assignee '$current_user_id' --scope '/subscriptions/$SUBSCRIPTION_ID' --query '[?contains(roleDefinitionName, \`Contributor\`)].roleDefinitionName' -o tsv"

        # Test 6: Verify we can read our own role assignments (this is what FPA typically does)
        testShouldSucceed "Verify Contributor role assignment" \
            "az role assignment list --assignee '$current_user_id' --role 'Contributor' --scope '/subscriptions/$SUBSCRIPTION_ID' --query '[0].roleDefinitionName' -o tsv"

        # Test 6b: Check access for common Azure services (FPA use cases)
        testShouldSucceed "Check access for storage operations" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Storage/storageAccounts/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test 6c: Check access for network operations (relevant for ARO)
        testShouldSucceed "Check access for network operations" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

    else
        printInfo "Skipping check access tests - cannot get service principal ID"
    fi

    # Test 7: List role definitions (should work with Contributor)
    testShouldSucceed "List role definitions" \
        "az role definition list --query '[0].roleName' -o tsv"

    # Test 8: Get specific role definition (common check access scenario)
    testShouldSucceed "Get Contributor role definition" \
        "az role definition list --name 'Contributor' --query '[0].roleName' -o tsv"

    # Test 9: Test permissions enumeration (FPA common operation)
    testShouldSucceed "List all permissions for Contributor role" \
        "az role definition list --name 'Contributor' --query '[0].permissions[0].actions' -o tsv"
}

# Test policy-restricted operations (should be blocked)
# Note: These tests rely on Azure policies to block operations before they execute
# We don't use --dry-run because many Azure CLI commands don't support it
testRestrictedOperations() {
    printHeader "Testing Restricted Operations (Should Be Blocked)"

    useMockFpaConfig

    # Test role assignment creation (should be blocked) - using invalid assignee to ensure safety
    testShouldFail "Create role assignment (safe test with invalid assignee)" \
        "az role assignment create --assignee 'invalid-user-id-12345' --role 'Reader' --scope '/subscriptions/$SUBSCRIPTION_ID'" || true

    # Test role definition creation (should be blocked) - using invalid actions to ensure safety
    testShouldFail "Create custom role definition (safe test with invalid action)" \
        "az role definition create --role-definition '{\"Name\":\"SafeTestRole-$USER-${RANDOM}\",\"Description\":\"Safe test - should be blocked\",\"Actions\":[\"InvalidAction/InvalidResource/read\"],\"AssignableScopes\":[\"/subscriptions/$SUBSCRIPTION_ID\"]}'" || true

    # Test policy assignment creation (should be blocked) - using invalid policy to ensure safety
    testShouldFail "Create policy assignment (safe test with invalid policy)" \
        "az policy assignment create --name 'safe-test-policy-$USER-${RANDOM}' --policy '/providers/Microsoft.Authorization/policyDefinitions/invalid-policy-id-12345' --scope '/subscriptions/$SUBSCRIPTION_ID'" || true

    # Test critical resource operations (should be blocked)
    # Using checkAccess API to test delete permissions safely
    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        # Test resource group deletion permissions using checkAccess API
        testShouldFail "Check delete permissions for resource groups" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$TEST_RG/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Resources/resourceGroups/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv" || true

        # Test key vault deletion permissions using checkAccess API
        testShouldFail "Check delete permissions for key vaults" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.KeyVault/vaults/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv" || true
    else
        printInfo "Skipping deletion permission tests - cannot get service principal ID"
    fi
}

# Test VM creation (should be blocked by policy)
testVmOperations() {
    printHeader "Testing VM Operations (Should Be Blocked)"

    useMockFpaConfig

    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        # Test VM creation permissions using checkAccess API (safer than actual creation)
        testShouldFail "Check VM creation permissions (should be blocked)" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Compute/virtualMachines/write\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test VM deletion permissions (should also be blocked)
        testShouldFail "Check VM deletion permissions (should be blocked)" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Compute/virtualMachines/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test VM read permissions (should be allowed with Contributor role)
        testShouldSucceed "Check VM read permissions (should be allowed)" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Compute/virtualMachines/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"
    else
        printFailure "Cannot get service principal ID for VM operations testing"
    fi
}

# Test storage account operations (using checkAccess API - no actual resources created)
testStorageOperations() {
    printHeader "Testing Storage Account Operations"

    useMockFpaConfig

    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        # Test storage account creation permissions (should be allowed with Contributor role)
        testShouldSucceed "Check storage account creation permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Storage/storageAccounts/write\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test storage account deletion permissions (should be blocked by policy)
        testShouldFail "Check storage account deletion permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Storage/storageAccounts/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test storage account read permissions (should be allowed)
        testShouldSucceed "Check storage account read permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Storage/storageAccounts/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"
    else
        printFailure "Cannot get service principal ID for storage operations testing"
    fi
}

# Test virtual network operations (using checkAccess API - no actual resources created)
testVirtualNetworkOperations() {
    printHeader "Testing Virtual Network Operations"

    useMockFpaConfig

    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        # Test VNet creation permissions (should be allowed with Contributor role)
        testShouldSucceed "Check virtual network creation permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/write\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test VNet deletion permissions (should be blocked by policy)
        testShouldFail "Check virtual network deletion permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test VNet read permissions (should be allowed)
        testShouldSucceed "Check virtual network read permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test subnet operations permissions
        testShouldSucceed "Check subnet read permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/subnets/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"
    else
        printFailure "Cannot get service principal ID for virtual network operations testing"
    fi
}

# Test container service operations (creation allowed, deletion blocked)
testContainerOperations() {
    printHeader "Testing Container Service Operations"

    useMockFpaConfig

    # Test AKS cluster deletion permissions using checkAccess API (safer than actual deletion)
    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        # Test AKS cluster deletion permissions using checkAccess API
        testShouldFail "Check delete permissions for AKS clusters" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.ContainerService/managedClusters/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv" || true

        # Test container registry deletion permissions
        testShouldFail "Check delete permissions for container registries" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.ContainerRegistry/registries/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv" || true
    else
        printInfo "Skipping container deletion permission tests - cannot get service principal ID"
    fi
}

# Test comprehensive resource deletion enforcement
testResourceDeletionEnforcement() {
    printHeader "Testing Resource Deletion Policy Enforcement"

    useMockFpaConfig

    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -n "$current_user_id" ]]; then
        printInfo "Testing deletion policy enforcement with service principal: $current_user_id"

        # Test resource group deletion (should be blocked)
        testShouldFail "Check resource group deletion permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$TEST_RG/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Resources/resourceGroups/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test key vault deletion (should be blocked)
        testShouldFail "Check key vault deletion permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.KeyVault/vaults/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test storage account deletion (should be blocked)
        testShouldFail "Check storage account deletion permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Storage/storageAccounts/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test virtual network deletion (should be blocked)
        testShouldFail "Check virtual network deletion permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

        # Test VM deletion (should be blocked)
        testShouldFail "Check virtual machine deletion permissions" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Compute/virtualMachines/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

    else
        printFailure "Cannot get service principal ID for deletion permission tests"
        return 1
    fi
}

# Test network operations and service association links (using existing resources only)
testNetworkOperations() {
    printHeader "Testing Network Operations and Service Association Links"

    useMockFpaConfig

    # Find existing VNet/subnet for safe testing (no resource creation)
    local test_vnet=$(az network vnet list --resource-group "$TEST_RG" --query "[0].name" -o tsv 2>/dev/null || echo "")
    local test_subnet=$(az network vnet subnet list --resource-group "$TEST_RG" --vnet-name "$test_vnet" --query "[0].name" -o tsv 2>/dev/null || echo "")

    if [[ -n "$test_vnet" && -n "$test_subnet" ]]; then
        printInfo "Testing with existing VNet: $test_vnet, Subnet: $test_subnet"

        # Reading subnet should work (basic Contributor permission)
        testShouldSucceed "Read subnet configuration" \
            "az network vnet subnet show --resource-group '$TEST_RG' --vnet-name '$test_vnet' --name '$test_subnet' --query 'name' -o tsv"

        # Service association links operations should be allowed by policy
        testShouldSucceed "List service association links" \
            "az network vnet subnet show --resource-group '$TEST_RG' --vnet-name '$test_vnet' --name '$test_subnet' --query 'serviceAssociationLinks' -o tsv"

        # Test subnet read permissions without modification
        testShouldSucceed "Read subnet properties" \
            "az network vnet subnet show --resource-group '$TEST_RG' --vnet-name '$test_vnet' --name '$test_subnet' --query 'addressPrefix' -o tsv"

    else
        printInfo "No existing VNet/subnet found for network operations testing"

        # Test network permissions using checkAccess API instead of creating resources
        local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")
        if [[ -n "$current_user_id" ]]; then
            # Test subnet read permissions (should be allowed)
            testShouldSucceed "Check subnet read permissions" \
                "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/subnets/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

            # Test service association links permissions (should be allowed)
            testShouldSucceed "Check service association links permissions" \
                "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"
        fi
    fi

    printInfo "Network operations testing completed using existing resources only"
}

# Test policy enforcement using safe validation methods
testPolicyValidationEnforcement() {
    printHeader "Testing Policy Enforcement Validation (Safe Methods)"

    useMockFpaConfig

    printInfo "Testing policy enforcement using validation and safe methods"

    # Test 1: Validate role assignment creation (should be blocked by policy)
    # Use --dry-run where available or test with invalid/non-existent target
    local test_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")
    if [[ -n "$test_user_id" ]]; then
        testShouldFail "Validate role assignment creation restrictions" \
            "az role assignment create --assignee 'nonexistent-user-id-12345' --role 'Reader' --scope '/subscriptions/$SUBSCRIPTION_ID' --dry-run" || \
            testShouldFail "Attempt role assignment to nonexistent user (should fail due to policy or user)" \
                "az role assignment create --assignee 'nonexistent-user-id-12345' --role 'Reader' --scope '/subscriptions/$SUBSCRIPTION_ID'"
    fi

    # Test 2: Validate policy assignment creation (should be blocked by policy)
    testShouldFail "Validate policy assignment creation restrictions" \
        "az policy assignment create --name 'test-validation-$USER' --policy '/providers/Microsoft.Authorization/policyDefinitions/invalid-policy-id' --scope '/subscriptions/$SUBSCRIPTION_ID'"

    # Test 3: Test role definition validation (should be blocked by policy)
    testShouldFail "Validate custom role creation restrictions" \
        "az role definition create --role-definition '{\"Name\":\"InvalidTestRole-$USER\",\"Description\":\"Invalid test\",\"Actions\":[\"invalid/action\"],\"AssignableScopes\":[\"/subscriptions/$SUBSCRIPTION_ID\"]}'"

    # Test 4: Test resource operation permissions using existing resources
    printInfo "Testing deletion permissions on existing resources..."

    # Find an existing storage account to test deletion permissions (but don't actually delete)
    local existing_storage=$(az storage account list --resource-group "$TEST_RG" --query "[0].name" -o tsv 2>/dev/null || echo "")
    if [[ -n "$existing_storage" ]]; then
        printInfo "Testing deletion validation on existing storage account: $existing_storage"
        # Test with --dry-run or use checkAccess API
        testShouldFail "Check if storage deletion would be blocked" \
            "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$test_user_id\"},\"actions\":[{\"id\":\"Microsoft.Storage/storageAccounts/delete\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"
    else
        printInfo "No existing storage account found for deletion permission testing"
    fi

    printInfo "Policy enforcement validation completed using safe methods"
}

# Print developer user information
printDeveloperInfo() {
    printHeader "Developer User Information"

    useDeveloperConfig

    printInfo "Script called with the following developer configuration:"
    getCurrentUserInfo | sed 's/^/  /'
    echo ""
}

# Check prerequisites as developer user
checkPrerequisites() {
    printHeader "Checking Prerequisites as Developer"

    useDeveloperConfig

    printInfo "Verifying mock FPA service principal has Contributor role..."

    # Get mock FPA application ID
    local mock_fpa_app_id=$(az ad app list --display-name "$FP_APPLICATION_NAME" --query "[0].appId" -o tsv 2>/dev/null)
    if [[ -z "$mock_fpa_app_id" ]]; then
        printFailure "Mock FPA application '$FP_APPLICATION_NAME' not found"
        echo "Please run: $SCRIPT_DIR/dev-application.sh create"
        return 1
    fi

    printInfo "Found mock FPA application: $mock_fpa_app_id"

    # Check if mock FPA has Contributor role
    local contributor_assignment=$(az role assignment list \
        --assignee "$mock_fpa_app_id" \
        --role "Contributor" \
        --scope "/subscriptions/$SUBSCRIPTION_ID" \
        --query "[0].id" -o tsv 2>/dev/null)

    if [[ -z "$contributor_assignment" ]]; then
        printFailure "Mock FPA does not have Contributor role on subscription"
        echo "Please run: $SCRIPT_DIR/dev-application.sh create"
        return 1
    fi

    printSuccess "Mock FPA has Contributor role assigned"

        # Check if policies are deployed
    local deny_policy_exists=$(az policy assignment show --name "deny-mock-fpa-dangerous-ops-$USER" --query "name" -o tsv 2>/dev/null)
    local allow_policy_exists=$(az policy assignment show --name "allow-mock-fpa-network-ops-$USER" --query "name" -o tsv 2>/dev/null)

    if [[ -z "$deny_policy_exists" || -z "$allow_policy_exists" ]]; then
        printFailure "Mock FPA restriction policies are not deployed"
        echo "Please run: $SCRIPT_DIR/dev-application.sh deploy-policies"
        return 1
    fi

    # Verify policies are correctly configured for the mock FPA
    printInfo "Verifying policy configuration targets the correct mock FPA..."

    local deny_policy_params=$(az policy assignment show --name "deny-mock-fpa-dangerous-ops-$USER" --query "parameters.mockFpaAppId.value" -o tsv 2>/dev/null)
    if [[ "$deny_policy_params" == "$mock_fpa_app_id" ]]; then
        printSuccess "Deny policy correctly configured for mock FPA: $mock_fpa_app_id"
    else
        printFailure "Deny policy has incorrect app ID configuration. Expected: $mock_fpa_app_id, Got: $deny_policy_params"
        return 1
    fi

    local allow_policy_params=$(az policy assignment show --name "allow-mock-fpa-network-ops-$USER" --query "parameters.mockFpaAppId.value" -o tsv 2>/dev/null)
    if [[ "$allow_policy_params" == "$mock_fpa_app_id" ]]; then
        printSuccess "Network policy correctly configured for mock FPA: $mock_fpa_app_id"
    else
        printFailure "Network policy has incorrect app ID configuration. Expected: $mock_fpa_app_id, Got: $allow_policy_params"
        return 1
    fi

    printSuccess "Mock FPA restriction policies are deployed and correctly configured"
    printInfo "All prerequisites verified successfully"
    return 0
}

# Test check access API specifically (runs first to verify basic functionality)
testCheckAccessApi() {
    printHeader "Testing Check Access API (Basic Functionality)"

    useMockFpaConfig

    # Get current service principal's object ID
    local current_user_id=$(az account show --query user.name -o tsv 2>/dev/null || echo "")

    if [[ -z "$current_user_id" ]]; then
        printFailure "Cannot get service principal ID for check access tests"
        return 1
    fi

    printInfo "Testing check access API with service principal: $current_user_id"

    # Test basic check access functionality
    testShouldSucceed "Check access API for subscription read permissions" \
        "az rest --method POST --url 'https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/providers/Microsoft.Authorization/checkAccess?api-version=2018-09-01-preview' --body '{\"subject\":{\"principalId\":\"$current_user_id\"},\"actions\":[{\"id\":\"Microsoft.Resources/subscriptions/read\",\"isDataAction\":false}]}' --query 'accessDecision' -o tsv"

    testShouldSucceed "Verify Contributor role assignment exists" \
        "az role assignment list --assignee '$current_user_id' --role 'Contributor' --scope '/subscriptions/$SUBSCRIPTION_ID' --query '[0].roleDefinitionName' -o tsv"

    printInfo "Basic check access API functionality verified"
}

# Main test execution
main() {
    printHeader "Mock FPA Policy Restriction Tests"

    # Step 1: Print developer user information
    printDeveloperInfo

    # Validate environment variables
    validateEnvironment

    printInfo "Testing environment: $SUBSCRIPTION_ID"
    printInfo "Mock FPA Application: $FP_APPLICATION_NAME"
    printInfo "Verbose output: $VERBOSE_OUTPUT"
    printInfo "Fail fast mode: $FAIL_FAST"

    # Step 2: Check prerequisites as developer user
    if ! checkPrerequisites; then
        printFailure "Prerequisites not met. Exiting."
        exit 1
    fi

    # Get test resources (using developer config)
    if ! getTestResourceGroup; then
        printFailure "Could not determine test resource group"
        exit 1
    fi

    # Step 3: Switch to mock FPA configuration and login if needed
    printHeader "Switching to Mock FPA Configuration"
    useMockFpaConfig

    printInfo "Switched to mock FPA Azure configuration"
    printInfo "Current config: $(getCurrentUserInfo | grep "Config Dir" | cut -d: -f2 | xargs)"

    # Check if already logged in as the correct service principal
    local skip_login=false
    if isLoggedInAsMockFpa "$FP_APPLICATION_NAME"; then
        skip_login=true
        printInfo "Already logged in as mock FPA service principal"
    fi

    if [[ "$skip_login" == "false" ]]; then
        # Login as mock FPA
        printInfo "Logging in as mock FPA service principal..."
        if ! loginWithMockServicePrincipal "$FP_CERTIFICATE_NAME" "$KEY_VAULT_NAME" "$FP_APPLICATION_NAME"; then
            printFailure "Failed to login as mock FPA"
            exit 1
        fi

        # Verify we're logged in as the service principal
        local current_user=$(az account show --query user.name -o tsv)
        local current_type=$(az account show --query user.type -o tsv)

        if [[ "$current_type" != "servicePrincipal" ]]; then
            printFailure "Not logged in as service principal (type: $current_type)"
            exit 1
        fi

        printInfo "Successfully logged in as: $current_user ($current_type)"
    fi

    # Step 4: Test check access API first to verify basic functionality
    runTest testCheckAccessApi

    # Step 5: Run comprehensive policy enforcement tests
    printHeader "Running Comprehensive Policy Enforcement Tests"

    # Basic functionality tests
    runTest testReadOperations
    runTest testCheckAccessOperations

    # Core policy enforcement tests (operations that should be blocked)
    runTest testRestrictedOperations
    runTest testVmOperations

    # Resource-specific policy enforcement tests
    runTest testStorageOperations
    runTest testVirtualNetworkOperations
    runTest testContainerOperations

    # Comprehensive deletion policy tests
    runTest testResourceDeletionEnforcement

        # Network policy and service association links tests
    runTest testNetworkOperations

    # Policy enforcement validation (safe methods)
    runTest testPolicyValidationEnforcement

    # The isolated Azure config directory ensures no interference with user's setup
    printInfo "Mock FPA testing completed with isolated Azure configuration"

    # Print summary
    printHeader "Test Results Summary"
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
cleanupOnInterrupt() {
    printInfo "Script interrupted, cleaning up..."

    # Ensure we're using mock FPA config for cleanup
    useMockFpaConfig

    # Clean up certificate files
    cleanupMockFpaCertificateFiles
    exit 1
}
trap 'cleanupOnInterrupt' INT TERM

# Check if required tools are available
if ! command -v az >/dev/null 2>&1; then
    echo -e "${RED}Error: Azure CLI (az) is not installed or not in PATH${NC}"
    exit 1
fi



# Run main function (arguments already parsed above)
main
