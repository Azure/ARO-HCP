#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source "$(dirname "$0")"/common.sh
source env_vars

if az_account_is_int; then
    az provider register --namespace "Microsoft.RedHatOpenShift"
else
    correlation_headers | curl --silent --show-error --include --header @- --request PUT "localhost:8443${SUBSCRIPTION_RESOURCE_ID}?api-version=2.0" --json "{\"state\":\"Registered\", \"registrationDate\": \"now\", \"properties\": { \"tenantId\": \"${TENANT_ID}\"}}"
fi
