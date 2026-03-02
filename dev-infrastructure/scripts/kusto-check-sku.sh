#!/bin/bash

set -x
set -o errexit
set -o nounset
set -o pipefail



if [ -z "${SKU:-}" ] || [ -z "${LOCATION:-}" ]; then
    echo "Error: SKU environment variable is required"
    echo "Usage: SKU=<sku> LOCATION=<location> ./kusto-check-sku.sh"
    exit 1
fi

az kusto cluster list-sku \
| jq --raw-output '.[] | select(.locations | index($ENV.LOCATION) != null) | .name' \
| grep "${SKU}"
