#!/bin/bash

source env_vars
source "$(dirname "$0")"/common.sh

rp_get_request "${SUBSCRIPTION_RESOURCE_ID}"
