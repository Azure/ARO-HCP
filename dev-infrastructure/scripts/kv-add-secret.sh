#!/bin/bash
set -eu

# Add a secret to a keyvault, grant required permissions
# Grant Secret Officer role to current user, if it is not set
# Write secret to keyvault
# Revoke Secret Officer role from current user, if it was added by this script

if [ "$#" -ne 4 ]; then
    echo "Usage: $0 <keyvault-name> <resource-group> <secret-name> <secret-value>"
    exit 1
fi

target_kv_name=$1
rg=$2
secret_name=$3
secret_value=$4

function officer_count() {
    az role assignment list --scope ${kv_id} --assignee ${currentuser_client_id} --role "Key Vault Secrets Officer" --output json | jq -r '.[].id'| wc -l
}

# Check and grant permissions
permission_granted=false
currentuser_client_id=$(az ad signed-in-user show -o json | jq -r '.id')
kv_id=$(az keyvault show --name ${target_kv_name} --resource-group ${rg} -o json | jq -r '.id')

asign_exists=$(officer_count)
if [ $asign_exists -eq 0 ]; then
    echo "Assigning Key Vault Secrets Officer role to current user"
    az role assignment create --assignee ${currentuser_client_id} --role "Key Vault Secrets Officer" --scope ${kv_id}
    permission_granted=true
fi

while [ $(officer_count) -eq 0 ]; do
    sleep 2
done

# Set secret
az keyvault secret set --vault-name ${target_kv_name} --name ${secret_name} --value "${secret_value}"

# Revoke secret permissions
if [ $permission_granted == "true" ]; then
    echo "Revoking Key Vault Secrets Officer role from current user"
    az role assignment delete --assignee ${currentuser_client_id} --role "Key Vault Secrets Officer" --scope ${kv_id}
fi
