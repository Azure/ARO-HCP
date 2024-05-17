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
APP_KEY_VAULT_NAME=${APP_KEY_VAULT_NAME:-"$UNIQUE_PREFIX-kv"}
APP_CERT_NAME=${APP_CERT_NAME:-"$UNIQUE_PREFIX-svc"}
APP_REGISTRATION_NAME=${APP_REGISTRATION_NAME:-"$UNIQUE_PREFIX-app"}
RESOURCE_GROUP=${RESOURCE_GROUP:-"$UNIQUE_PREFIX-RG"}
ROLE_DEFINITION_NAME=${ROLE_DEFINITION_NAME:-"$UNIQUE_PREFIX-role"}
SUBSCRIPTION_ID=${SUBSCRIPTION_ID:-$(az account show --query id -o tsv)}

printEnv() {
    echo "LOCATION: $LOCATION"
    echo "APP_KEY_VAULT_NAME: $APP_KEY_VAULT_NAME"
    echo "APP_CERT_NAME: $APP_CERT_NAME"
    echo "APP_REGISTRATION_NAME: $APP_REGISTRATION_NAME"
    echo "RESOURCE_GROUP: $RESOURCE_GROUP"
    echo "SUBSCRIPTION_ID: $SUBSCRIPTION_ID"
}

shellEnv() {
    # Calling shell can "eval" this output.
    echo "LOCATION=\"$LOCATION\"; export LOCATION"
    echo "APP_KEY_VAULT_NAME=\"$APP_KEY_VAULT_NAME\"; export APP_KEY_VAULT_NAME"
    echo "APP_CERT_NAME=\"$APP_CERT_NAME\"; export APP_CERT_NAME"
    echo "APP_REGISTRATION_NAME=\"$APP_REGISTRATION_NAME\"; export APP_REGISTRATION_NAME"
    echo "RESOURCE_GROUP=\"$RESOURCE_GROUP\"; export RESOURCE_GROUP"
    echo "SUBSCRIPTION_ID=\"$SUBSCRIPTION_ID\"; export SUBSCRIPTION_ID"
}

createMockFirstPartyApp() {
    echo "Creating a standalone dev application with the following ENV:"
    printEnv
    if ! [ -x "$(command -v jq)" ]; then
        echo "jq is required to run this script"
        exit 1
    fi
    
    echo "Creating resource group: $RESOURCE_GROUP"
    az group create \
    --name "$RESOURCE_GROUP" \
    --location "$LOCATION"
    
    echo "Creating keyvault: $APP_KEY_VAULT_NAME"
    az keyvault create \
    --location "$LOCATION" \
    --name "$APP_KEY_VAULT_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --enable-rbac-authorization false
    
    echo "checking if certificate: $APP_CERT_NAME exists"
    certExists=$(az keyvault certificate list --vault-name $APP_KEY_VAULT_NAME --query "[?name=='$APP_CERT_NAME'].name" -o tsv)
    if [ -n "$certExists" ]; then
        echo "Certificate already exists"
        exit 1
    else
        echo "Certificate does not exist"
        echo "Creating certificate: $APP_CERT_NAME"
        az keyvault certificate create \
        --vault-name "$APP_KEY_VAULT_NAME" \
        --name "$APP_CERT_NAME" \
        --policy "$(az keyvault certificate get-default-policy)"
    fi
    
    # Create a custom role defintion if it doesn't exist already
    echo "checking if role definition: $ROLE_DEFINITION_NAME exists"
    roleExists=$(az role definition list --name "$ROLE_DEFINITION_NAME" --query "[0].name" -o tsv)
    
    if [ -n "$roleExists" ]; then
        echo "Role definition already exists"
    else
        echo "Role definition does not exist"
        # add assignable scope to the custom role with the current subscription
        roleDef=$(jq ".AssignableScopes = [\"/subscriptions/$SUBSCRIPTION_ID\"]" mock-dev-role-definition.json)
        echo $roleDef >> temp.json
        roleDef=$(jq ".Name = \"$ROLE_DEFINITION_NAME\"" temp.json)
        rm temp.json
        
        echo "creating role definition: $ROLE_DEFINITION_NAME \n $roleDef\n"
        az role definition create --role-definition "$roleDef"
        while [ -z "$roleExists" ]; do
            roleExists=$(az role definition list --name "$ROLE_DEFINITION_NAME" --query "[0].name" -o tsv)
            echo "waiting for role definition to be created"
            sleep 5
        done
    fi
    
    
    echo "creating app registration: $APP_REGISTRATION_NAME"
    az ad sp create-for-rbac \
    --display-name "$APP_REGISTRATION_NAME" \
    --keyvault "$APP_KEY_VAULT_NAME" \
    --cert "$APP_CERT_NAME" \
    --role "$ROLE_DEFINITION_NAME" \
    --scopes "/subscriptions/$SUBSCRIPTION_ID"
}

deleteMockFirstPartyApp() {
    echo "Deleting the standalone dev application with the following ENV:"
    printEnv
    
    echo "deleting all role assignments with role: $ROLE_DEFINITION_NAME"
    az role assignment list --role "$ROLE_DEFINITION_NAME" --query "[].id" -o tsv | xargs -I {} az role assignment delete --ids {}
    
    spId="$(az ad sp list --display-name "$APP_REGISTRATION_NAME" --query "[0].id" -o tsv)"
    echo "deleting sp with id: $spId"
    az ad sp delete --id "$spId"
    
    appId="$(az ad app list --display-name "$APP_REGISTRATION_NAME" --query "[0].appId" -o tsv)"
    echo "deleting app with id: $appId"
    az ad app delete --id "$appId"
    
    echo "deleting role definition: $ROLE_DEFINITION_NAME"
    az role definition delete --name "$ROLE_DEFINITION_NAME"
    
    echo "deleting keyvault: $APP_KEY_VAULT_NAME in resource group: $RESOURCE_GROUP"
    az keyvault delete --name "$APP_KEY_VAULT_NAME" --resource-group "$RESOURCE_GROUP"
    
    echo "purging keyvault: $APP_KEY_VAULT_NAME"
    az keyvault purge --name "$APP_KEY_VAULT_NAME"
    
    echo "delete resource group: $RESOURCE_GROUP"
    az group delete --name "$RESOURCE_GROUP" --yes
}

loginWithMockServicePrincipal() {
    az keyvault secret download \
    --name "$APP_CERT_NAME" \
    --vault-name "$APP_KEY_VAULT_NAME" \
    --encoding base64 \
    --file app.pfx
    
    openssl pkcs12 \
    -in app.pfx \
    -passin pass: \
    -out app.pem \
    -nodes
    
    appId=$(az ad app list --display-name "$APP_REGISTRATION_NAME" --query "[0].appId" -o tsv)
    tenantId=$(az account show --query tenantId -o tsv)
    
    az login --service-principal -u "$appId" -p app.pem --tenant "$tenantId"

    rm app.pfx app.pem
}

case "$1" in
    "create")
        createMockFirstPartyApp
    ;;
    "delete")
        deleteMockFirstPartyApp
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
