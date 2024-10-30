#!/bin/bash

RESOURCEGROUP=$1
DB_SERVER_NAME_PREFIX=$2
MANAGED_IDENTITY_NAME=$3
NAMESPACE=$4
SA_NAME=$5
DB_NAME=$6

# prep creds and configs
export PGHOST=$(az postgres flexible-server list --resource-group ${RESOURCEGROUP} --query "[?starts_with(name, '${DB_SERVER_NAME_PREFIX}')].fullyQualifiedDomainName" -o tsv)
AZURE_TENANT_ID=$(az account show -o json | jq .homeTenantId -r)
AZURE_CLIENT_ID=$(az identity show -g ${RESOURCEGROUP} -n ${MANAGED_IDENTITY_NAME} --query clientId -o tsv)
SA_TOKEN=$(kubectl create token ${SA_NAME} --duration=1h --namespace=${NAMESPACE} --audience api://AzureADTokenExchange)

# az login with managed identity via SA token
export AZURE_CONFIG_DIR="${HOME}/.azure-profile-cs-${RESOURCEGROUP}"
rm -rf $AZURE_CONFIG_DIR
az login --federated-token ${SA_TOKEN} --service-principal -u $AZURE_CLIENT_ID -t $AZURE_TENANT_ID > /dev/null 2>&1

# get tmp DB password
export PGPASSWORD=$(az account get-access-token --resource='https://ossrdbms-aad.database.windows.net' -o json | jq .accessToken -r)
rm -rf $AZURE_CONFIG_DIR

export PGUSER=${MANAGED_IDENTITY_NAME}
podman run -it --rm \
    --env PGHOST \
    --env PGUSER \
    --env PGPASSWORD \
    postgres \
    psql \
    -d $DB_NAME -p 5432
