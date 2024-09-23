#!/bin/bash

# Get certificate from a keyvaultS, grant required permissions
# Grant Certificates Officer role to current user for target kv, if it is not set
# Write certificate to keyvault
# Revoke Certificates Officer role from current user and target kv, if it was added by this script

if [ "$#" -ne 4 ]; then
    echo "Usage: $0 <source-kv-name> <resource-group> <secret-name> <target-kv-value>"
    exit 1
fi

source_kv_name=$1
rg=$2
secret_name=$3
target_kv_name=$4

function officer_cert_count() {
    az role assignment list --scope ${target_kv_id} --assignee ${currentuser_client_id} --role "Key Vault Certificates Officer" | jq -r '.[].id'| wc -l
}
# Check and grant permissions
permission_granted=false
currentuser_client_id=$(az ad signed-in-user show -o json | jq -r '.id')
source_kv_id=$(az keyvault show --name ${source_kv_name} --query id -o tsv)
target_kv_id=$(az keyvault show --name ${target_kv_name} --resource-group ${rg} -o json | jq -r '.id')


asign_exists=$(officer_cert_count)
if [ $asign_exists -eq 0 ]; then
    echo "Assigning Key Vault Certificate Officer role to current user"
    az role assignment create --assignee ${currentuser_client_id} --role "Key Vault Certificates Officer" --scope ${target_kv_id}
    permission_granted=true
fi

while [ $(officer_cert_count) -eq 0 ]; do
    sleep 2
done

# Get the certificate from source kv
az keyvault secret show --vault-name ${source_kv_name} --name ${secret_name} --query "value" -o tsv | base64 -d > /tmp/${secret_name}.pfx

# Set certificate on target kv
az keyvault certificate import --vault-name ${target_kv_name} --name ${secret_name} --file /tmp/${secret_name}.pfx

# Remove temporary cert
rm /tmp/${secret_name}.pfx

# Revoke secret permissions
if [ $permission_granted == "true" ]; then
    echo "Revoking Key Vault Certificates Officer role from current user"
    az role assignment delete --assignee ${currentuser_client_id} --role "Key Vault Certificates Officer" --scope ${target_kv_id}
fi
