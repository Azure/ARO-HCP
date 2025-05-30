#!/bin/bash

# What is this? Shell curl wrapper for the purposes of ARO HCP Setup:
#
# - includes various headers based on the requrest
# - selects endpoint
# - shows the request (via `set -o xtrace`) so that one can copy paste it into
#   command line to rerun it if needed

#
# Default configuration
#

# TODO: allow to defined this
FRONTEND_HOST=localhost
FRONTEND_PORT=8443
# ADMINAPI_HOST=localhost
# ADMINAPI_POST=TODO

# Global array variable for HTTP headers (values are added there as
# needed via varius http header functions defined below).
declare -a HTTP_HEADERS

#
# Functions
#

show_help()
{
  echo -e "Usage: $(basename "$0") [options] METHOD LOCATION\n"
  echo "Options:
  -H      define additional HTTP header
  -c      this is create request, add X-Ms-Arm-Resource-System-Data header
  -t      test mode, just show headers for given request
  -v      verbose mode (for manual debugging and CI runs)
  -d      dry run (don't send the http request, for testing only)
  -h      this message"
}

arm_x_ms_identity_url_header()
{
  # Requests directly against the frontend
  # need to send a X-Ms-Identity-Url HTTP
  # header, which simulates what ARM performs.
  # By default we set a dummy value, which is
  # enough in the environments where a real
  # Managed Identities Data Plane does not
  # exist like in the development or integration
  # environments. The default can be overwritten
  # by providing the environment variable
  # ARM_X_MS_IDENTITY_URL when running the script.
  : "${ARM_X_MS_IDENTITY_URL:="https://dummyhost.identity.azure.net"}"
  HTTP_HEADERS+=("X-Ms-Identity-Url: ${ARM_X_MS_IDENTITY_URL}")
}

arm_system_data_header()
{
  # try to use actual username
  if [[ ! ${DRY_RUN} -eq 1 ]]; then
    USER_DATA=$(az account list -o json | jq -r '.[]|select(.isDefault==true)|.user')
    USER_NAME=$(jq -r '.name' <<< "${USER_DATA}")
    USER_TYPE=$(jq -r '.type' <<< "${USER_DATA}")
    CREATED_TS=$(date -u +"%Y-%m-%dT%H:%M:%S+00:00")
  else
    USER_NAME=shadowman@example.com
    USER_TYPE=user
    CREATED_TS=2020-10-20T20:10:20+00:00
  fi
  HTTP_HEADERS+=("X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"${USER_NAME}\", \"createdByType\": \"${USER_TYPE}\", \"createdAt\": \"${CREATED_TS}\"}")
}

correlation_headers()
{
  if [ -n "$(which uuidgen 2> /dev/null)" ]; then
    HTTP_HEADERS+=("X-Ms-Correlation-Request-Id: $(uuidgen)")
    HTTP_HEADERS+=("X-Ms-Client-Request-Id: $(uuidgen)")
    HTTP_HEADERS+=("X-Ms-Return-Client-Request-Id: true")
  fi
}

print_headers()
{
  printf '%s\n' "${HTTP_HEADERS[@]}"
}

#
# main
#

if [[ $# = 0 ]]; then
  show_help
  exit
fi

while getopts "vthH:cd" OPT; do
  case "${OPT}" in
  c) IS_CREATE=1;;
  v) DEBUG=1;;
  d) DRY_RUN=1;;
  t) SHOW_HEADERS=1;;
  H) HTTP_HEADERS+=("$OPTARG");;
  h) show_help; exit;;
  *) show_help; exit 1;;
  esac
done

shift $((OPTIND-1))

if [[ $# -lt 2 ]]; then
  echo "http METHOD and request LOCATION not specified" >&2
  show_help
  exit 1
fi

METHOD=$1
LOCATION=$2
shift 2

# if the location value can start with a slash, we drop it to avoid double
# slash in the resulting url
if [[ "${LOCATION}" =~ ^\/.* ]]; then
  LOCATION=${LOCATION:1}
fi

# TODO: implement selection later
API_HOST=${FRONTEND_HOST}
API_PORT=${FRONTEND_PORT}

# TODO: validate assumption about headers!
# These headers are mandatory, used for every request.
correlation_headers
arm_x_ms_identity_url_header
# These headers are used for create requests only
if [[ ${IS_CREATE} ]]; then
  arm_system_data_header
fi

# Testing mode: just show what headers will be used and exit
if [[ ${SHOW_HEADERS} ]]; then
  print_headers
  exit 0
fi

# Dry run mode: replace curl with a simple wrapper
if [[ ${DRY_RUN} -eq 1 ]]; then
curl()
{
  # print arguments
  echo curl "$@"
  # receive and print http headers
  cat
}
fi

# Verbose mode: bash tracing
if [[ ${DEBUG} -eq 1 ]]; then
  set -o xtrace
fi

print_headers | curl ${DEBUG:+"--include"} -H @- --request "${METHOD}" "${API_HOST}:${API_PORT}/${LOCATION}" "$@"
