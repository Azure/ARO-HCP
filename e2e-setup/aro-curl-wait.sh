#!/bin/bash

#
# Defaults
#

STATUS_QUERY='.properties.provisioningState'
SLEEP_TIME=120
MAX_TIME=1800

#
# Functions
#

show_help()
{
  echo "usage: $(basename "$0") [command-options] azurepath state"
  echo
  echo "$(basename "$0"): wait for a resource in given path to reach given state"
  echo
  echo "options:"
  echo " -s		sleep time between iterations (default ${SLEEP_TIME} sec)"
  echo " -t		timeout (default ${MAX_TIME} sec)"
  echo " -q		redefine status query (default ${STATUS_QUERY})"
  echo " -h		show this help message and exit"
}

#
# main
#

if [[ $# -lt 2 ]]; then
  show_help
  exit
fi

while getopts "vhq:s:t:" OPT; do
  case "${OPT}" in
  q) STATUS_QUERY="${OPTARG}";;
  s) SLEEP_TIME="${OPTARG}";;
  t) MAX_TIME="${OPTARG}";;
  h) show_help; exit;;
  *) show_help; exit 1;;
  esac
done

shift $((OPTIND-1))

AZURE_PATH=$1
TARGET_STATE=$2

# Create a temporary file for cluster status
TMP_FILE=$(mktemp)

TS_START=$(date +%s)

while true; do
  ./arocurl.sh GET "${AZURE_PATH}" > "${TMP_FILE}"
  TS=$(date +%s)
  TS_STR=$(date -u +"%Y-%m-%dT%H:%M:%S+00:00" --date="@${TS}")
  STATE=$(jq --raw-output "${STATUS_QUERY}" "${TMP_FILE}")
  if [[ "${STATE}" = "${TARGET_STATE}" ]]; then
    echo "${TS_STR} $AZURE_PATH reached ${TARGET_STATE}"
    break
  fi
  if [[ "${TS}" -gt $(("${TS_START}" + "${MAX_TIME}")) ]]; then
    echo "${TS_STR} $AZURE_PATH in ${STATE} after ${MAX_TIME} seconds"
    echo timeout
    exit 1
  fi
  echo "${TS_STR} $AZURE_PATH is in ${STATE}, waiting for ${TARGET_STATE}"
  sleep "${SLEEP_TIME}"
done

rm "${TMP_FILE}"
