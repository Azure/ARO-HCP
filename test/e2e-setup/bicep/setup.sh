#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

tags=(persist=false)
job_id="${BUILD_ID:-}"
if [[ -n "${job_id//[[:space:]]/}" ]]; then
  tags+=("jobID.aro-hcp-ci.redhat.com=${BUILD_ID}")
fi

az group create \
  --name "${CUSTOMER_RG_NAME}" \
  --subscription "${SUBSCRIPTION}" \
  --location "${LOCATION}" \
  --tags "${tags[@]}"

az deployment group create \
  --name 'aro-hcp-e2e-setup' \
  --subscription "${SUBSCRIPTION}" \
  --resource-group "${CUSTOMER_RG_NAME}" \
  --template-file "${SETUP_FILE}" \
  --parameters \
      persistTagValue=false \
      clusterName="${CLUSTER_NAME}"
