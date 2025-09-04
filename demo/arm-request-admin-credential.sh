#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

# Create a temporary file to the async operation location
tmp_file=$(mktemp)
trap "rm -f $tmp_file" EXIT

# Create a temporary file to the async operation output
tmp_output=$(mktemp)
trap "rm -f $tmp_output" EXIT

# Request the admin credential
az rest --method POST \
    --uri "${CLUSTER_RESOURCE_ID}/requestadmincredential?api-version=2024-06-10-preview" --debug 2>&1 2> $tmp_file

# Wait for the async operation to complete
while true; do
    az rest --method GET \
        --uri "$(grep Location $tmp_file | grep -o '/subscription.*' |tr -d \')" > ${tmp_output}
    if grep -q 'kubeconfig' ${tmp_output}; then
        break
    fi
    sleep 1
done

cat ${tmp_output} | jq -r '.kubeconfig'
