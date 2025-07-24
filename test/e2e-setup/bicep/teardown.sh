#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

az group delete --name "${CUSTOMER_RG_NAME}" -y
