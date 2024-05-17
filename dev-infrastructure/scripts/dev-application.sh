#!/bin/bash

# This script can be used to spin up a standalone dev application which will be used as a 'mock first party application'.
# This is required due to the lack of the ability to have a first party app be used in the dev tenant

LOCATION=${LOCATION:-"eastus"}
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

printEnv() {
    echo "LOCATION: $LOCATION"
    echo "RESOURCE_GROUP: $RESOURCE_GROUP"
    echo "SUBSCRIPTION_ID: $SUBSCRIPTION_ID"
    echo "KEY_VAULT_NAME: $KEY_VAULT_NAME"
    echo "FP_APPLICATION_NAME: $FP_APPLICATION_NAME"
    echo "FP_CERTIFICATE_NAME: $FP_CERTIFICATE_NAME"
    echo "AH_APPLICATION_NAME: $AH_APPLICATION_NAME"
    echo "AH_CERTIFICATE_NAME: $AH_CERTIFICATE_NAME"
}

shellEnv() {
    # Calling shell can "eval" this output.
    echo "LOCATION=\"$LOCATION\"; export LOCATION"
    echo "RESOURCE_GROUP=\"$RESOURCE_GROUP\"; export RESOURCE_GROUP"
    echo "SUBSCRIPTION_ID=\"$SUBSCRIPTION_ID\"; export SUBSCRIPTION_ID"
    echo "ARO_HCP_DEV_KEY_VAULT_NAME=\"$KEY_VAULT_NAME\"; export ARO_HCP_DEV_KEY_VAULT_NAME"
    echo "ARO_HCP_DEV_FP_APPLICATION_NAME=\"$FP_APPLICATION_NAME\"; export ARO_HCP_DEV_FP_APPLICATION_NAME"
    echo "ARO_HCP_DEV_FP_CERTIFICATE_NAME=\"$FP_CERTIFICATE_NAME\"; export ARO_HCP_DEV_FP_CERTIFICATE_NAME"
    echo "ARO_HCP_DEV_AH_APPLICATION_NAME=\"$AH_APPLICATION_NAME\"; export ARO_HCP_DEV_AH_APPLICATION_NAME"
    echo "ARO_HCP_DEV_AH_CERTIFICATE_NAME=\"$FP_CERTIFICATE_NAME\"; export ARO_HCP_DEV_AH_CERTIFICATE_NAME"
}

createServicePrincipal() {
    APPLICATION_NAME=$1
    CERTIFICATE_NAME=$2
    ROLE_DEFINITION_NAME=$3

    certExists=$(az keyvault certificate list --vault-name $KEY_VAULT_NAME --query "[?name=='$CERTIFICATE_NAME'].name" -o tsv)
    if [ -n "$certExists" ]; then
        echo "Certificate $CERTIFICATE_NAME already exists"
        exit 1
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

createApps() {
    echo "Creating standalone dev applications with the following ENV:"
    printEnv
    if ! [ -x "$(command -v jq)" ]; then
        echo "jq is required to run this script"
        exit 1
    fi

    echo "Creating resource group $RESOURCE_GROUP"
    az group create \
    --name "$RESOURCE_GROUP" \
    --location "$LOCATION"

    echo "Creating keyvault $KEY_VAULT_NAME"
    az keyvault create \
    --location "$LOCATION" \
    --name "$KEY_VAULT_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --enable-rbac-authorization false

    # Create a custom role defintion if it doesn't exist already
    echo "Checking if role definition $FP_ROLE_DEFINITION_NAME exists"
    roleExists=$(az role definition list --name "$FP_ROLE_DEFINITION_NAME" --query "[0].name" -o tsv)

    if [ -n "$roleExists" ]; then
        echo "Role definition $FP_ROLE_DEFINITION_NAME already exists"
    else
        # add assignable scope to the custom role with the current subscription
        roleDef=$(jq ".AssignableScopes = [\"/subscriptions/$SUBSCRIPTION_ID\"]" mock-dev-role-definition.json)
        echo $roleDef >> temp.json
        roleDef=$(jq ".Name = \"$FP_ROLE_DEFINITION_NAME\"" temp.json)
        rm temp.json

        echo "Creating role definition $FP_ROLE_DEFINITION_NAME"
        echo "$roleDef"
        az role definition create --role-definition "$roleDef"
        while [ -z "$roleExists" ]; do
            roleExists=$(az role definition list --name "$FP_ROLE_DEFINITION_NAME" --query "[0].name" -o tsv)
            echo "Waiting for role definition to be created..."
            sleep 5
        done
    fi

    createServicePrincipal $FP_APPLICATION_NAME $FP_CERTIFICATE_NAME $FP_ROLE_DEFINITION_NAME
    createServicePrincipal $AH_APPLICATION_NAME $AH_CERTIFICATE_NAME $AZURE_BUILTIN_ROLE_OWNER
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

    echo "Deleting all role assignments with role $FP_ROLE_DEFINITION_NAME"
    az role assignment list --role "$FP_ROLE_DEFINITION_NAME" --query "[].id" -o tsv | xargs -I {} az role assignment delete --ids {}

    deleteServicePrincipalAndApp $FP_APPLICATION_NAME
    deleteServicePrincipalAndApp $AH_APPLICATION_NAME

    echo "Deleting role definition $FP_ROLE_DEFINITION_NAME"
    az role definition delete --name "$FP_ROLE_DEFINITION_NAME"

    echo "Deleting keyvault $KEY_VAULT_NAME in resource group $RESOURCE_GROUP"
    az keyvault delete --name "$KEY_VAULT_NAME" --resource-group "$RESOURCE_GROUP"

    echo "Purging keyvault $KEY_VAULT_NAME"
    az keyvault purge --name "$KEY_VAULT_NAME"

    echo "Deleting resource group $RESOURCE_GROUP"
    az group delete --name "$RESOURCE_GROUP" --yes
}

loginWithMockServicePrincipal() {
    az keyvault secret download \
    --name "$FP_CERTIFICATE_NAME" \
    --vault-name "$KEY_VAULT_NAME" \
    --encoding base64 \
    --file app.pfx

    openssl pkcs12 \
    -in app.pfx \
    -passin pass: \
    -out app.pem \
    -nodes

    appId=$(az ad app list --display-name "$FP_APPLICATION_NAME" --query "[0].appId" -o tsv)
    tenantId=$(az account show --query tenantId -o tsv)

    az login --service-principal -u "$appId" -p app.pem --tenant "$tenantId"

    rm app.pfx app.pem
}

case "$1" in
    "create")
        createApps
    ;;
    "delete")
        deleteApps
    ;;
    "login")
        loginWithMockServicePrincipal
    ;;
    "shell")
        shellEnv
    ;;
    *)
        echo "Usage: $0 {create|delete|login|shell}"
        exit 1
    ;;
esac
