#!/bin/bash

# prep creds and configs
RESOURCEGROUP=aro-hcp-svc-cluster-${USER}
PGHOST=$(az postgres flexible-server list --resource-group ${RESOURCEGROUP} --query "[?starts_with(name, 'cs-pg-')].fullyQualifiedDomainName" -o tsv)
PGUSER="clusters-service"
AZURE_TENANT_ID=64dc69e4-d083-49fc-9569-ebece1dd1408
AZURE_CLIENT_ID=$(az identity show -g ${RESOURCEGROUP} -n clusters-service --query clientId -o tsv)
SA_TOKEN=$(kubectl create token clusters-service --namespace=cluster-service --audience api://AzureADTokenExchange)

# az login with managed identity via SA token
export AZURE_CONFIG_DIR="${HOME}/.azure-profile-cs-${RESOURCEGROUP}"
rm -rf $AZURE_CONFIG_DIR
az login --federated-token ${SA_TOKEN} --service-principal -u $AZURE_CLIENT_ID -t $AZURE_TENANT_ID

# get tmp DB password
PGPASSWORD=$(az account get-access-token --resource='https://ossrdbms-aad.database.windows.net' | jq .accessToken -r)

psql -d clusters-service
