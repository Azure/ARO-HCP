#!/bin/bash

# This is not part of the E2E Test Setup (we assume the subscription is already
# registered), but can be useful when initializing ARO HCP Personal DEV
# Environment (see docs/personal-dev.md).

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

REG_TS=$(date -u +"%Y-%m-%d")

./arocurl.sh -v -c \
  PUT "/subscriptions/${CUSTOMER_SUBSCRIPTION}?api-version=2.0" \
  --json "{\"state\":\"Registered\", \"registrationDate\": \"${REG_TS}\", \"properties\": { \"tenantId\": \"${CUSTOMER_TENANT}\"}}"
