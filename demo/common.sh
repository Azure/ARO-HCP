#!/bin/bash

header() {
    if az_account_is_int; then
        echo -n "${1}=${2} "
    else
        echo "${1}: ${2}"
    fi
}

arm_system_data_header() {
    header X-Ms-Arm-Resource-System-Data "{\"createdBy\": \"${USER}\", \"createdByType\": \"User\", \"createdAt\": \"$(date -u +"%Y-%m-%dT%H:%M:%S+00:00")\"}"
}

arm_x_ms_identity_url_header() {
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
  : ${ARM_X_MS_IDENTITY_URL:="https://dummyhost.identity.azure.net"}
  header X-Ms-Identity-Url "${ARM_X_MS_IDENTITY_URL}"
}

correlation_headers() {
    if [ -n "$(which uuidgen 2> /dev/null)" ]; then
        header X-Ms-Correlation-Request-Id "$(uuidgen)"
        header X-Ms-Client-Request-Id "$(uuidgen)"
        header X-Ms-Return-Client-Request-Id "true"
    fi
}

rp_get_request() {
    # Arguments:
    # $1 = Request URL path
    # $2 = (optional) API version
    URL="${1}?api-version=${2:-${FRONTEND_API_VERSION}}"
    if az_account_is_int; then
        az rest --headers "$(correlation_headers)" --url "${URL}"
    else
        correlation_headers | curl --silent --show-error --header @- "localhost:8443${URL}"
    fi
}

rp_put_request() {
    # Arguments:
    # $1 = Request URL path
    # $2 = Request JSON body
    # $3 = (optional) API version
    URL="${1}?api-version=${3:-${FRONTEND_API_VERSION}}"
    if az_account_is_int; then
        az rest --method put --headers "$(arm_system_data_header; correlation_headers; arm_x_ms_identity_url_header)" --url "${URL}" --body "${2}"
    else
        (arm_system_data_header; correlation_headers; arm_x_ms_identity_url_header) | curl --silent --show-error --include --header @- --request PUT "localhost:8443${URL}" --json "${2}"
    fi
}

rp_delete_request() {
    # Arguments:
    # $1 = Request URL path
    # $2 = (optional) API version
    URL="${1}?api-version${2:-${FRONTEND_API_VERSION}}"
    if az_account_is_int; then
        az rest --method delete --headers "$(arm_system_data_header; correlation_headers)" --url "${URL}"
    else
        (arm_system_data_header; correlation_headers) | curl --silent --show-error --include --header @- --request DELETE "localhost:8443${URL}"
    fi
}
