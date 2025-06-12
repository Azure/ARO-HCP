#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

rp_post_request "${CLUSTER_RESOURCE_ID}/revokeCredentials"
