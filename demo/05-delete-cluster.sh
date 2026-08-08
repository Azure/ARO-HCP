#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

rp_delete_request "${CLUSTER_RESOURCE_ID}"