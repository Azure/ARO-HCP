#!/bin/bash

# This script can be used to spin up a standalone dev application which will be used as a 'mock first party application'.
# This is required due to the lack of the ability to have a first party app be used in the dev tenant

LOCATION="eastus"
UNIQUE_PREFIX="HCP-$USER-$LOCATION"
APP_KEY_VAULT_NAME="$UNIQUE_PREFIX-vlt"
APP_CERT_NAME="$UNIQUE_PREFIX-svc"
APP_REGISTRATION_NAME="$UNIQUE_PREFIX-app"
RESOURCE_GROUP="$UNIQUE_PREFIX-RG"
ROLE_DEFINITION_NAME="$UNIQUE_PREFIX Role"
SUBSCRIPTION_ID=$(az account show --query id -o tsv)

printEnv() {
    echo "LOCATION: $LOCATION"
    echo "APP_KEY_VAULT_NAME: $APP_KEY_VAULT_NAME"
    echo "APP_CERT_NAME: $APP_CERT_NAME"
    echo "APP_REGISTRATION_NAME: $APP_REGISTRATION_NAME"
    echo "RESOURCE_GROUP: $RESOURCE_GROUP"
    echo "SUBSCRIPTION_ID: $SUBSCRIPTION_ID"
}

createMockFirstPartyApp() {
    echo "Creating a standalone dev application with the following ENV:"
    printEnv
    if ! [ -x "$(command -v jq)" ]; then
        echo "jq is required to run this script"
        exit 1
    fi
    
    # Create a resource group to hold the key vault
    az group create \
    --name "$RESOURCE_GROUP" \
    --location "$LOCATION"
    
    # Create a keyvault to hold the certificate
    az keyvault create \
    --location "$LOCATION" \
    --name "$APP_KEY_VAULT_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --enable-rbac-authorization false
    
    # Create a certificate in the keyvault
    az keyvault certificate create \
    --vault-name "$APP_KEY_VAULT_NAME" \
    --name "$APP_CERT_NAME" \
    --policy "$(az keyvault certificate get-default-policy)"
    
    # Create a custom role defintion if it doesn't exist already
    # TODO: check if it exists
    
    # add assignable scope to the custom role with the current subscription
    roleDef=$(jq ".AssignableScopes = [\"/subscriptions/$SUBSCRIPTION_ID\"]" dev-role-definition.json)
    echo $roleDef >> temp.json
    roleDef=$(jq ".Name = \"$ROLE_DEFINITION_NAME\"" temp.json)
    rm temp.json
    
    az role definition create --role-definition "$roleDef"
    
    # Create an app registration and adds a Service Pricipal using the certificate
    az ad sp create-for-rbac \
    --display-name "$APP_REGISTRATION_NAME" \
    --keyvault "$APP_KEY_VAULT_NAME" \
    --cert "$APP_CERT_NAME" \
    --role "ARO HCP Dev Role" \
    --scopes "/subscriptions/$SUBSCRIPTION_ID"
}

deleteMockFirstPartyApp() {
    echo "Deleting the standalone dev application with the following ENV:"
    printEnv
    az ad sp delete --id "$(az ad sp list --display-name "$APP_REGISTRATION_NAME" --query "[0].id" -o tsv)"
    az ad app delete --id "$(az ad app list --display-name "$APP_REGISTRATION_NAME" --query "[0].id" -o tsv)"
    az role definition delete --name "$ROLE_DEFINITION_NAME"
    az keyvault delete --name "$APP_KEY_VAULT_NAME" --resource-group "$RESOURCE_GROUP"
    az keyvault purge --name "$APP_KEY_VAULT_NAME"
    az group delete --name "$RESOURCE_GROUP" --yes
}

case "$1" in
    "create")
        createMockFirstPartyApp
        ;;
    "delete")
        deleteMockFirstPartyApp
        ;;
    *)
        echo "Usage: $0 {create|delete}"
        exit 1
        ;;
esac