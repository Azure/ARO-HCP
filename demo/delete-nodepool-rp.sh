#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

# The last node pool can not be deleted from a cluster. See https://issues.redhat.com/browse/ARO-21617 for more details.

rp_delete_request "${NODE_POOL_RESOURCE_ID}"
