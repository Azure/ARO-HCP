#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source "$(dirname "$0")"/common.sh
source env_vars

if is_int_testing_subscription || is_stg_testing_subscription || is_prod_testing_subscription; then
    az provider register --namespace "Microsoft.RedHatOpenShift"
else
    rp_put_request "${SUBSCRIPTION_RESOURCE_ID}" "{\"state\":\"Registered\", \"registrationDate\": \"now\", \"properties\": { \"tenantId\": \"${TENANT_ID}\"}}" "2.0"
fi
