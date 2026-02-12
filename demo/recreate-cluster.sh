#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

CLUSTER_FILE="cluster.json"
rp_put_request "${CLUSTER_RESOURCE_ID}" "@${CLUSTER_FILE}"
