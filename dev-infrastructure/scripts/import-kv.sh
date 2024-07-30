#!/bin/bash

# Set the names of your source and target Key Vaults
SOURCE_KV_NAME=$1
TARGET_KV_NAME=$2

# List all secrets in the source Key Vault and loop through them
for SECRET_NAME in $(az keyvault secret list --vault-name $SOURCE_KV_NAME --query "[].id" -o tsv | xargs -n1 basename); do
    # Retrieve the secret value from the source Key Vault
    SECRET_VALUE=$(az keyvault secret show --name $SECRET_NAME --vault-name $SOURCE_KV_NAME --query "value" -o tsv)
    # Create or update the secret in the target Key Vault
    az keyvault secret set --vault-name $TARGET_KV_NAME --name $SECRET_NAME --value "$SECRET_VALUE"
done
