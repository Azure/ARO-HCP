#!/bin/bash

set -euo pipefail

CLUSTER_NAME=$1
shift
NAMESPACE=$1
shift
SA_NAME=$1
shift

KUBECONFIG=$(mktemp)
hcpctl sc breakglass "${CLUSTER_NAME}" --output "${KUBECONFIG}" --no-shell

AZURE_TENANT_ID=$(az account show -o json | jq .homeTenantId -r)
AZURE_CLIENT_ID=$(kubectl get sa -n aro-hcp-admin-api admin-api -o yaml | yq '.metadata.annotations."azure.workload.identity/client-id"' -r)
SA_TOKEN=$(kubectl create token "${SA_NAME}" --namespace="${NAMESPACE}" --audience api://AzureADTokenExchange)

export AZURE_CONFIG_DIR="${HOME}/.azure-profile-admin-api"
rm -rf "${AZURE_CONFIG_DIR}"
az login --federated-token "${SA_TOKEN}" --service-principal -u "${AZURE_CLIENT_ID}" -t "${AZURE_TENANT_ID}"

# run the rest of the command with $*
exec "$@"