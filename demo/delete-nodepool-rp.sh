#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

# The last node pool can not be deleted from a cluster. See https://issues.redhat.com/browse/XCMSTRAT-1069 for more details.

(arm_system_data_header; correlation_headers) | curl --silent --show-error --include --request DELETE "localhost:8443${NODE_POOL_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}" \
  --header @-
