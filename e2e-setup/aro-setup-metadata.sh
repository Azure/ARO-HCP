#!/bin/bash

# What is this?
# A simple shell script to include additional metadata and details into a
# minimal e2e setup json file.
# Expected to be used in setup scripts to create e2e json metadata files.

#
# Functions
#

show_help()
{
  echo "usage: $(basename "$0") [command-options] input_file output_file"
  echo
  echo "$(basename "$0"): include additional details into e2e setup json metadata"
  echo
  echo "options:"
  echo " -h		show this help message and exit"
  echo " -v		verbose mode"
}

#
# main
#

if [[ $# != 2 ]]; then
  show_help
  exit
fi

while getopts "vh" OPT; do
  case "${OPT}" in
  v) DEBUG=1;;
  h) show_help; exit;;
  *) show_help; exit 1;;
  esac
done

INPUT_FILE=$1
OUTPUT_FILE=$2

shift $((OPTIND-1))

if [[ "${DEBUG}" -eq 1 ]]; then
  set -o xtrace
fi

# Create a temporary file
TMP_FILE=$(mktemp)

# Validate and save the input json into TMP_FILE
cat "${INPUT_FILE}" > "${TMP_FILE}" || exit 1
if ! jq '.' "${TMP_FILE}" >/dev/null; then
  echo "$0: syntax error found in the intpun json" >&2
  nl "${TMP_FILE}"
  rm "${TMP_FILE}"
  exit 1
fi

# Include mandatory details about customer enviroment
jq \
  --arg customer_rg_name    "${CUSTOMER_RG_NAME}" \
  --arg customer_vnet_name  "${CUSTOMER_VNET_NAME}" \
  --arg customer_nsg_name   "${CUSTOMER_NSG_NAME}" \
  ' .customer_env.customer_rg_name   = $customer_rg_name |
    .customer_env.customer_vnet_name = $customer_vnet_name |
    .customer_env.customer_nsg_name  = $customer_nsg_name
  ' \
  "${TMP_FILE}" > "${OUTPUT_FILE}"
cat "${OUTPUT_FILE}" > "${TMP_FILE}"

# Include data about managed identities if present
if [[ -f "${UAMIS_JSON_FILENAME}" && -f "${IDENTITY_UAMIS_JSON_FILENAME}" ]]; then
  jq \
    --argjson uamis_data           "$(cat "${UAMIS_JSON_FILENAME}")" \
    --argjson identity_uamis_data  "$(cat "${IDENTITY_UAMIS_JSON_FILENAME}")" \
    ' .customer_env.uamis = $uamis_data |
      .customer_env.identity_uamis = $identity_uamis_data
    ' \
    "${TMP_FILE}" > "${OUTPUT_FILE}"
  cat "${OUTPUT_FILE}" > "${TMP_FILE}"
else
  echo "$0: UAMIS json files not found, MI data not included" >&2
fi

# Include data about the cluster if present
if [[ -f "${CLUSTER_JSON_FILENAME}" ]]; then
  jq \
    --argjson cluster_data "$(cat "${CLUSTER_JSON_FILENAME}")" \
    ' .cluster.armdata = $cluster_data ' \
    "${TMP_FILE}" > "${OUTPUT_FILE}"
  cat "${OUTPUT_FILE}" > "${TMP_FILE}"
else
  echo "$0: ${CLUSTER_JSON_FILENAME} not found, cluser data not included" >&2
fi

# Include data for each nodepool
for NODEPOOL_NAME in $(jq -r '.nodepools[].name' "${TMP_FILE}"); do
  # we assume that the nodepool json files are in the same directory as the
  # cluster json files
  DATA_DIR=$(dirname "${CLUSTER_JSON_FILENAME}")
  NODEPOOL_JSON_FILENAME=${DATA_DIR}/${CLUSTER_NAME}.${NODEPOOL_NAME}.nodepool.json
  jq \
    --argjson nodepool_data "$(cat "${NODEPOOL_JSON_FILENAME}")" \
    " .nodepools |= map(
      if .name == \"${NODEPOOL_NAME}\"
        then . + {\"armdata\": \$nodepool_data }
        else . 
      end
    )" \
    "${TMP_FILE}" > "${OUTPUT_FILE}"
    cat "${OUTPUT_FILE}" > "${TMP_FILE}"
done

rm "${TMP_FILE}"
