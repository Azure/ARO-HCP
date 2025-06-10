#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

rp_get_request "${NODE_POOL_RESOURCE_ID}"
