#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source "$(dirname "$0")"/common.sh
source env_vars

if az_account_is_int; then
    az provider register --namespace "Microsoft.RedHatOpenShift"
else
    rp_put_request "${SUBSCRIPTION_RESOURCE_ID}" "{\"state\":\"Registered\", \"registrationDate\": \"now\", \"properties\": { \"tenantId\": \"${TENANT_ID}\"}}" "2.0"
fi
