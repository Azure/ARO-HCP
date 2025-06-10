#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

correlation_headers | curl --silent --header @- "localhost:8443${NODE_POOL_RESOURCE_ID}?${FRONTEND_API_VERSION_QUERY_PARAM}" | jq
