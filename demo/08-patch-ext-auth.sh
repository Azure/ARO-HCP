#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

EXTERNAL_AUTH_TMPL_FILE="external_auth_patch.tmpl.json"
EXTERNAL_AUTH_FILE="external_auth_patch.json"


jq '.' "${EXTERNAL_AUTH_TMPL_FILE}" > ${EXTERNAL_AUTH_FILE}

cat ${EXTERNAL_AUTH_FILE}

rp_patch_request "${EXTERNAL_AUTH_RESOURCE_ID}" "@${EXTERNAL_AUTH_FILE}"
