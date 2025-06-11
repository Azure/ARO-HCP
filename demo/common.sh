#!/bin/bash

header() {
    echo "${1}: ${2}"
}

authorization_header() {
    if [ ! -v ACCESS_TOKEN ]; then
        ACCESS_TOKEN=$(az account get-access-token --query accessToken --output tsv)
    fi
    header Authorization "Bearer ${ACCESS_TOKEN}"
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
    URL="${FRONTEND_HOST}${1}?api-version=${2:-${FRONTEND_API_VERSION}}"
    case "${FRONTEND_HOST}" in
        *localhost*)
            HEADERS=$(correlation_headers)
            ;;
        *)
            HEADERS=$(authorization_header)
            ;;
    esac
    echo "${HEADERS}" | curl --silent --show-error --header @- "${URL}"
}

rp_put_request() {
    # Arguments:
    # $1 = Request URL path
    # $2 = Request JSON body
    # $3 = (optional) API version
    URL="${FRONTEND_HOST}${1}?api-version=${3:-${FRONTEND_API_VERSION}}"
    case "${FRONTEND_HOST}" in
        *localhost*)
            HEADERS=$(arm_system_data_header; correlation_headers; arm_x_ms_identity_url_header)
            ;;
        *)
            HEADERS=$(authorization_header)
            ;;
    esac
    echo "${HEADERS}" | curl --silent --show-error --include --header @- --request PUT "${URL}" --json "${2}"
}

rp_delete_request() {
    # Arguments:
    # $1 = Request URL path
    # $2 = (optional) API version
    URL="${FRONTEND_HOST}${1}?api-version=${2:-${FRONTEND_API_VERSION}}"
    case "${FRONTEND_HOST}" in
        *localhost*)
            HEADERS=$(arm_system_data_header; correlation_headers)
            ;;
        *)
            HEADERS=$(authorization_header)
            ;;
    esac
    echo "${HEADERS}" | curl --silent --show-error --include --header @- --request DELETE "${URL}"
}
