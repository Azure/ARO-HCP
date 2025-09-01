#!/bin/bash

# This script can be used to spin up a standalone dev application which will be used as a 'mock first party application'.
# This is required due to the lack of the ability to have a first party app be used in the dev tenant
#
# This script uses a dedicated Azure CLI config directory (mock-fpa-azure-config) to avoid interfering
# with the user's existing Azure CLI configuration.

LOCATION=${LOCATION:-"westus3"}
UNIQUE_PREFIX=${UNIQUE_PREFIX:-"HCP-$USER-$LOCATION"}
# Microsoft.KeyVault has the shortest name length limit of 24 characters.
# We restrict the prefix to 21 characters to allow room for the "-kv" suffix.
# See https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules
if [ ${#UNIQUE_PREFIX} -gt 21 ]; then
    echo "UNIQUE_PREFIX=$UNIQUE_PREFIX is too long" > /dev/stderr
    UNIQUE_PREFIX=${UNIQUE_PREFIX:0:21}
    echo "trimmed UNIQUE_PREFIX=$UNIQUE_PREFIX to 21 characters" > /dev/stderr
fi

RESOURCE_GROUP=${RESOURCE_GROUP:-"$UNIQUE_PREFIX-RG"}
SUBSCRIPTION_ID=${SUBSCRIPTION_ID:-$(az account show --query id -o tsv)}

KEY_VAULT_NAME=${ARO_HCP_DEV_KEY_VAULT_NAME:-"$UNIQUE_PREFIX-kv"}

# Mock first-party application (limited permissions)
FP_APPLICATION_NAME=${ARO_HCP_DEV_FP_APPLICATION_NAME:-"$UNIQUE_PREFIX-fp-app"}
FP_CERTIFICATE_NAME=${ARO_HCP_DEV_FP_CERTIFICATE_NAME:-"$UNIQUE_PREFIX-fp-cert"}
FP_ROLE_DEFINITION_NAME=${ARO_HCP_DEV_FP_ROLE_DEFINITION_NAME:-"$UNIQUE_PREFIX-fp-role"}

# ARM helper application (subscription owner, simulates ARM)
AH_APPLICATION_NAME=${ARO_HCP_DEV_AH_APPLICATION_NAME:-"$UNIQUE_PREFIX-ah-app"}
AH_CERTIFICATE_NAME=${ARO_HCP_DEV_AH_CERTIFICATE_NAME:-"$UNIQUE_PREFIX-ah-cert"}

# See https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles
AZURE_BUILTIN_ROLE_OWNER="8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
AZURE_BUILTIN_ROLE_CONTRIBUTOR="b24988ac-6180-42a0-ab88-20f7382dd24c"

# Get script directory and source common functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/mock-fpa-common.sh"

# Set up Azure config directory and file paths
setupAzureConfig "$SCRIPT_DIR"

printEnv() {
    printMockFpaEnv "$LOCATION" "$RESOURCE_GROUP" "$SUBSCRIPTION_ID" "$KEY_VAULT_NAME" \
        "$FP_APPLICATION_NAME" "$FP_CERTIFICATE_NAME" "$AH_APPLICATION_NAME" "$AH_CERTIFICATE_NAME"
}

shellEnv() {
    # Calling shell can "eval" this output.
    exportMockFpaShellEnv "$LOCATION" "$RESOURCE_GROUP" "$SUBSCRIPTION_ID" "$KEY_VAULT_NAME" \
        "$FP_APPLICATION_NAME" "$FP_CERTIFICATE_NAME" "$AH_APPLICATION_NAME" "$AH_CERTIFICATE_NAME"
}

createServicePrincipal() {
    APPLICATION_NAME=$1
    CERTIFICATE_NAME=$2
    ROLE_DEFINITION_NAME=$3

    # Check if application already exists
    appExists=$(az ad app list --display-name "$APPLICATION_NAME" --query "[0].appId" -o tsv)
    if [ -n "$appExists" ]; then
        echo "Application $APPLICATION_NAME already exists (App ID: $appExists)"

        # Check if service principal exists
        spExists=$(az ad sp list --display-name "$APPLICATION_NAME" --query "[0].id" -o tsv)
        if [ -n "$spExists" ]; then
            echo "Service principal for $APPLICATION_NAME already exists"
        else
            echo "Creating service principal for existing application $APPLICATION_NAME"
            az ad sp create --id "$appExists"
        fi

        # Check role assignment
        roleAssignmentExists=$(az role assignment list \
            --assignee "$appExists" \
            --role "$ROLE_DEFINITION_NAME" \
            --scope "/subscriptions/$SUBSCRIPTION_ID" \
            --query "[0].id" -o tsv)

        if [ -n "$roleAssignmentExists" ]; then
            echo "Role assignment for $APPLICATION_NAME already exists"
        else
            echo "Creating role assignment for $APPLICATION_NAME"
            az role assignment create \
                --assignee "$appExists" \
                --role "$ROLE_DEFINITION_NAME" \
                --scope "/subscriptions/$SUBSCRIPTION_ID"
        fi

        return
    fi

    # Check if certificate exists
    certExists=$(az keyvault certificate list --vault-name $KEY_VAULT_NAME --query "[?name=='$CERTIFICATE_NAME'].name" -o tsv)
    if [ -n "$certExists" ]; then
        echo "Certificate $CERTIFICATE_NAME already exists"
    else
        echo "Creating certificate $CERTIFICATE_NAME"
        az keyvault certificate create \
        --vault-name "$KEY_VAULT_NAME" \
        --name "$CERTIFICATE_NAME" \
        --policy "$(az keyvault certificate get-default-policy)"
    fi

    echo "Creating service principal for $APPLICATION_NAME"
    az ad sp create-for-rbac \
    --display-name "$APPLICATION_NAME" \
    --keyvault "$KEY_VAULT_NAME" \
    --cert "$CERTIFICATE_NAME" \
    --role "$ROLE_DEFINITION_NAME" \
    --scopes "/subscriptions/$SUBSCRIPTION_ID"
}

deployMockFpaPolicies() {
    echo "Deploying mock FPA restriction policies"

    # Get the application ID of the mock FPA
    mockFpaAppId=$(az ad app list --display-name "$FP_APPLICATION_NAME" --query "[0].appId" -o tsv)

    if [ -z "$mockFpaAppId" ]; then
        echo "Error: Could not find application ID for $FP_APPLICATION_NAME"
        exit 1
    fi

    echo "Checking policies for mock FPA with app ID: $mockFpaAppId"

    # Check if policy definitions already exist
    denyPolicyExists=$(az policy definition show --name "deny-mock-fpa-dangerous-ops-$USER" --query "name" -o tsv 2>/dev/null)
    allowPolicyExists=$(az policy definition show --name "allow-mock-fpa-required-network-ops-$USER" --query "name" -o tsv 2>/dev/null)

    # Check if policy assignments already exist
    denyAssignmentExists=$(az policy assignment show --name "deny-mock-fpa-dangerous-ops-$USER" --query "name" -o tsv 2>/dev/null)
    allowAssignmentExists=$(az policy assignment show --name "allow-mock-fpa-network-ops-$USER" --query "name" -o tsv 2>/dev/null)

    if [ -n "$denyPolicyExists" ] && [ -n "$allowPolicyExists" ] && [ -n "$denyAssignmentExists" ] && [ -n "$allowAssignmentExists" ]; then
        echo "Mock FPA restriction policies already exist and are assigned"
        return
    fi

    echo "Deploying/updating policies for mock FPA"

    # Deploy the Bicep template at subscription scope
    az deployment sub create \
        --location "$LOCATION" \
        --template-file "$(dirname "$0")/../modules/policy/mock-fpa-restrictions.bicep" \
        --parameters \
            mockFpaAppId="$mockFpaAppId" \
            environment="$USER" \
            enforcementEnabled=true
}

deleteMockFpaPolicies() {
    echo "Deleting mock FPA restriction policies"

    # Delete policy assignments first
    az policy assignment delete --name "deny-mock-fpa-dangerous-ops-$USER" 2>/dev/null || echo "Policy assignment deny-mock-fpa-dangerous-ops-$USER not found or already deleted"
    az policy assignment delete --name "allow-mock-fpa-network-ops-$USER" 2>/dev/null || echo "Policy assignment allow-mock-fpa-network-ops-$USER not found or already deleted"

    # Delete policy definitions
    az policy definition delete --name "deny-mock-fpa-dangerous-ops-$USER" 2>/dev/null || echo "Policy definition deny-mock-fpa-dangerous-ops-$USER not found or already deleted"
    az policy definition delete --name "allow-mock-fpa-required-network-ops-$USER" 2>/dev/null || echo "Policy definition allow-mock-fpa-required-network-ops-$USER not found or already deleted"
}

createApps() {
    echo "Creating standalone dev applications with the following ENV (idempotent):"
    printEnv
    if ! [ -x "$(command -v jq)" ]; then
        echo "jq is required to run this script"
        exit 1
    fi

    # Check if resource group exists
    if az group show --name "$RESOURCE_GROUP" &>/dev/null; then
        echo "Resource group $RESOURCE_GROUP already exists"
    else
        echo "Creating resource group $RESOURCE_GROUP"
        az group create \
        --name "$RESOURCE_GROUP" \
        --location "$LOCATION"
    fi

    # Check if key vault exists
    if az keyvault show --name "$KEY_VAULT_NAME" &>/dev/null; then
        echo "Key vault $KEY_VAULT_NAME already exists"
    else
        echo "Creating keyvault $KEY_VAULT_NAME"
        az keyvault create \
        --location "$LOCATION" \
        --name "$KEY_VAULT_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --enable-rbac-authorization false
    fi

    # NOTE: Using built-in Contributor role instead of custom role to support check access APIs
    # Custom role creation is commented out as we now use AZURE_BUILTIN_ROLE_CONTRIBUTOR
    #
    # # Create a custom role defintion if it doesn't exist already
    # echo "Checking if role definition $FP_ROLE_DEFINITION_NAME exists"
    # roleExists=$(az role definition list --name "$FP_ROLE_DEFINITION_NAME" --query "[0].name" -o tsv)
    #
    # if [ -n "$roleExists" ]; then
    #     echo "Role definition $FP_ROLE_DEFINITION_NAME already exists"
    # else
    #     # add assignable scope to the custom role with the current subscription
    #     roleDef=$(jq ".AssignableScopes = [\"/subscriptions/$SUBSCRIPTION_ID\"]" mock-dev-role-definition.json)
    #     echo $roleDef >> temp.json
    #     roleDef=$(jq ".Name = \"$FP_ROLE_DEFINITION_NAME\"" temp.json)
    #     rm temp.json
    #
    #     echo "Creating role definition $FP_ROLE_DEFINITION_NAME"
    #     echo "$roleDef"
    #     az role definition create --role-definition "$roleDef"
    #     while [ -z "$roleExists" ]; do
    #         roleExists=$(az role definition list --name "$FP_ROLE_DEFINITION_NAME" --query "[0].name" -o tsv)
    #         echo "Waiting for role definition to be created..."
    #         sleep 5
    #     done
    # fi

    createServicePrincipal $FP_APPLICATION_NAME $FP_CERTIFICATE_NAME $AZURE_BUILTIN_ROLE_CONTRIBUTOR
    createServicePrincipal $AH_APPLICATION_NAME $AH_CERTIFICATE_NAME $AZURE_BUILTIN_ROLE_OWNER

    # Deploy restriction policies for the mock FPA
    deployMockFpaPolicies
}

deleteServicePrincipalAndApp() {
    APPLICATION_NAME=$1

    spId=$(az ad sp list --display-name "$APPLICATION_NAME" --query "[0].id" -o tsv)
    if [ -n "$spId" ]; then
        echo "Deleting service principal for $APPLICATION_NAME"
        az ad sp delete --id "$spId"
    fi

    appId=$(az ad app list --display-name "$APPLICATION_NAME" --query "[0].appId" -o tsv)
    if [ -n "$appId" ]; then
        echo "Deleting application $APPLICATION_NAME"
        az ad app delete --id $(az ad app list --display-name "$APPLICATION_NAME" --query "[0].appId" -o tsv)
    fi
}

deleteApps() {
    echo "Deleting standalone dev applications with the following ENV:"
    printEnv

    # Delete mock FPA restriction policies first
    deleteMockFpaPolicies

    echo "Deleting all role assignments with role $FP_ROLE_DEFINITION_NAME"
    az role assignment list --role "$FP_ROLE_DEFINITION_NAME" --query "[].id" -o tsv | xargs -I {} az role assignment delete --ids {} 2>/dev/null || echo "No role assignments found for $FP_ROLE_DEFINITION_NAME"

    deleteServicePrincipalAndApp $FP_APPLICATION_NAME
    deleteServicePrincipalAndApp $AH_APPLICATION_NAME

    echo "Deleting role definition $FP_ROLE_DEFINITION_NAME"
    az role definition delete --name "$FP_ROLE_DEFINITION_NAME" 2>/dev/null || echo "Role definition $FP_ROLE_DEFINITION_NAME not found or already deleted"

    echo "Deleting keyvault $KEY_VAULT_NAME in resource group $RESOURCE_GROUP"
    az keyvault delete --name "$KEY_VAULT_NAME" --resource-group "$RESOURCE_GROUP"

    echo "Purging keyvault $KEY_VAULT_NAME"
    az keyvault purge --name "$KEY_VAULT_NAME"

    echo "Deleting resource group $RESOURCE_GROUP"
    az group delete --name "$RESOURCE_GROUP" --yes
}

# Use shared functions for service principal operations

case "$1" in
    "create")
        createApps
    ;;
    "delete")
        deleteApps
    ;;
    "login")
        loginWithMockServicePrincipal "$FP_CERTIFICATE_NAME" "$KEY_VAULT_NAME" "$FP_APPLICATION_NAME"
    ;;
    "shell")
        shellEnv
    ;;
    "deploy-policies")
        deployMockFpaPolicies
    ;;
    "delete-policies")
        deleteMockFpaPolicies
    ;;
    "cleanup")
        cleanupMockFpaCertificateFiles
    ;;
    *)
        echo "Usage: $0 {create|delete|login|shell|deploy-policies|delete-policies|cleanup}"
        exit 1
    ;;
esac
