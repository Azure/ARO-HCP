#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

az group create \
  --name "${CUSTOMER_RG_NAME}" \
  --subscription "${SUBSCRIPTION}" \
  --location "${LOCATION}" \
  --tags persist=false

az deployment group create \
  --name 'aro-hcp-e2e-setup' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file "${BICEP_FILE}" \
  --parameters \
      persistTagValue=false
      # clusterName="${CLUSTER_NAME}"
