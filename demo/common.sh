#!/bin/bash

header() {
    echo "${1}: ${2}"
}

authorization_header() {
    if [ -z "${ACCESS_TOKEN:-}" ]; then
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

async_operation_status() {
    # Arguments:
    # $1 = URL
    # $2 = Headers
    OUTPUT=$(echo "${2}" | curl --silent --header @- ${1})
    STATUS=$(echo $OUTPUT | jq -r '.status')
    echo "${OUTPUT}"
    case ${STATUS} in
        Succeeded | Failed | Canceled)
            return 1
            ;;
        *)
            return 0
            ;;
    esac
}

# Export the function so "watch" can see it.
export -f async_operation_status

rp_request() {
    # Arguments:
    # $1 = HTTP method
    # $2 = URL
    # $3 = Headers
    # $4 = (optional) JSON body
    case ${1} in
        GET)
            CMD="curl --silent --show-error --header @- ${2}"
            ;;
        POST)
            CMD="curl --silent --show-error --include --header @- --request ${1} ${2} --json ''"
            ;;
        *)
            CMD="curl --silent --show-error --include --header @- --request ${1} ${2}"
            if [ $# -ge 4 ]; then
                CMD+=" --json '${4}'"
            fi
            ;;
    esac
    OUTPUT=$(echo "${3}" | eval ${CMD} | tr -d '\r')
    ASYNC_STATUS_ENDPOINT=$(echo "${OUTPUT}" | awk 'tolower($1) ~ /^azure-asyncoperation:/ {print $2}')
    ASYNC_RESULT_ENDPOINT=$(echo "${OUTPUT}" | awk 'tolower($1) ~ /^location:/ {print $2}')

    # If a status endpoint header is present, watch the
    # endpoint until the status reaches a terminal state.
    if [ -n "${ASYNC_STATUS_ENDPOINT}" ]; then
        watch --errexit --exec bash -c "async_operation_status \"${ASYNC_STATUS_ENDPOINT}\" \"${3}\" 2> /dev/null" || true
        if [ -n "${ASYNC_RESULT_ENDPOINT}" ]; then
            FULL_RESULT=$(echo "${3}" | curl --silent --show-error --include --header @- "${ASYNC_RESULT_ENDPOINT}")
            JSON_RESULT=$(echo "${FULL_RESULT}" | tr -d '\r' | jq -Rs 'split("\n\n")[1] | fromjson?')

            # If the response body is JSON, try to extract and write a kubeconfig file.
            KUBECONFIG=$(echo "${JSON_RESULT}" | jq -r '.kubeconfig')
            if [ -n "$KUBECONFIG" ]; then
                echo "${KUBECONFIG}" > kubeconfig
                echo "Wrote kubeconfig"
            else
                echo "${FULL_RESULT}"
            fi
        else
            echo "${OUTPUT}"
        fi
    else
        echo "${OUTPUT}"
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
    rp_request GET "${URL}" "${HEADERS}"
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
    rp_request PUT "${URL}" "${HEADERS}" "${2}"
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
    rp_request DELETE "${URL}" "${HEADERS}"
}

rp_post_request() {
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
    rp_request POST "${URL}" "${HEADERS}"
}
