#!/bin/bash

set -euo pipefail

CLUSTER_NAME=$1
shift
NAMESPACE=$1
shift
SA_NAME=$1
shift

KUBECONFIG=$(mktemp)

cleanup() {
  echo "Cleanup kubeconfig dir ${KUBECONFIG} ..."
	rm -rf "${KUBECONFIG}"
}
trap cleanup EXIT INT TERM

${HCPCTL} sc breakglass "${CLUSTER_NAME}" --output "${KUBECONFIG}" --no-shell

AZURE_TENANT_ID=$(az account show -o json | jq .homeTenantId -r)
AZURE_CLIENT_ID=$(kubectl get sa -n aro-hcp-admin-api admin-api -o yaml | yq '.metadata.annotations."azure.workload.identity/client-id"' -r)
AZURE_SA_TOKEN=$(kubectl create token "${SA_NAME}" --namespace="${NAMESPACE}" --audience api://AzureADTokenExchange)
KUBE_SA_TOKEN=$(kubectl create token "${SA_NAME}" --namespace="${NAMESPACE}")

export AZURE_CONFIG_DIR="${HOME}/.azure-profile-admin-api"
rm -rf "${AZURE_CONFIG_DIR}"
az login --federated-token "${AZURE_SA_TOKEN}" --service-principal -u "${AZURE_CLIENT_ID}" -t "${AZURE_TENANT_ID}"

# Add a new context to the kubeconfig that uses the service account token for Kubernetes auth
KUBE_CONTEXT_NAME=$(kubectl config current-context --kubeconfig="${KUBECONFIG}")
KUBE_CLUSTER_NAME=$(kubectl config view --kubeconfig="${KUBECONFIG}" -o jsonpath="{.contexts[?(@.name==\"${KUBE_CONTEXT_NAME}\")].context.cluster}")

# Create a new user entry with the SA token
kubectl config set-credentials "${SA_NAME}" --kubeconfig="${KUBECONFIG}" --token="${KUBE_SA_TOKEN}"

# Create a new context that uses the SA credentials
SA_CONTEXT_NAME="${KUBE_CONTEXT_NAME}-sa"
kubectl config set-context "${SA_CONTEXT_NAME}" --kubeconfig="${KUBECONFIG}" --cluster="${KUBE_CLUSTER_NAME}" --user="${SA_NAME}"

# Use the SA context by default
kubectl config use-context "${SA_CONTEXT_NAME}" --kubeconfig="${KUBECONFIG}"

echo $KUBECONFIG

KUBECONFIG="${KUBECONFIG}" "$@"
