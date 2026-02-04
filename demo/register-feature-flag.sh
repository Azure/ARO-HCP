#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source "$(dirname "$0")"/common.sh
source env_vars

if [ -z "${FFLAG}" ]; then
  echo "Error: FFLAG variable is empty. Please provide a feature flag name."
  exit 1
fi

# Re-register the subscription with the AFEC flag passed by parameter
rp_put_request "${SUBSCRIPTION_RESOURCE_ID}" "{
  \"state\": \"Registered\",
  \"registrationDate\": \"now\",
  \"properties\": {
    \"tenantId\": \"${TENANT_ID}\",
    \"registeredFeatures\": [
      {
        \"name\": \"Microsoft.RedHatOpenShift/${FFLAG}\",
        \"state\": \"Registered\"
      }
    ]
  }
}" "2.0"
