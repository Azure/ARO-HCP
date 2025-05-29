#!/bin/bash

# Simple set of tests for arocurl wrapper running in dry run mode (no network
# communication is happening during the run).

source test/env.test
source env.defaults

# In case of any failure, the script will immediatelly exit with error.
set -o errexit

# Name of the file with setup e2e metadata
SETUP_FILENAME=$(mktemp)

#
# Test Cases
#

echo "Test: Infra Only"
./aro-setup-metadata.sh - "${SETUP_FILENAME}" 2>/dev/null << EOF
{
  "e2e_setup": {
    "name": "infra-only",
    "tags": ["customer-infra-only"]
  },
  "cluster": {},
  "nodepools": []
}
EOF
OUT=$(jq '.customer_env' "${SETUP_FILENAME}")
EXP='{
  "customer_rg_name": "test-aro-hcp-rg",
  "customer_vnet_name": "aro-hcp-vnet",
  "customer_nsg_name": "aro-hcp-nsg"
}
'
diff <(echo $EXP) <(echo $OUT)

echo "Test: Cluster with nodepools"
# Overriding some defaults with dummy files
export UAMIS_JSON_FILENAME=test/empty.json
export IDENTITY_UAMIS_JSON_FILENAME=test/empty.json
export CLUSTER_JSON_FILENAME=test/empty.json
# Creating files for nodepools
./aro-setup-metadata.sh - "${SETUP_FILENAME}" << EOF
{
  "e2e_setup": {
    "name": "cluster-demo",
    "tags": []
  },
  "cluster": {
    "name": "${CLUSTER_NAME}"
  },
  "nodepools": [
    {
      "name": "pool-one"
    },
    {
      "name": "pool-two"
    }
  ]
}
EOF
EXP='{
  "value": "one"
}'
OUT=$(jq '.nodepools[]|select(.name=="pool-one")|.armdata' "${SETUP_FILENAME}")
diff <(echo $EXP) <(echo $OUT)
EXP='{
  "value": "two"
}'
OUT=$(jq '.nodepools[]|select(.name=="pool-two")|.armdata' "${SETUP_FILENAME}")
diff <(echo $EXP) <(echo $OUT)


#
# Teardown
#

#rm "${SETUP_FILENAME}"
