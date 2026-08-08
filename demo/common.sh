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

rp_get_request() {
    # Arguments:
    # $1 = Resource ID
    local resource_id=$1
    local headers=""
    headers="${headers}$(authorization_header)\n"
    headers="${headers}$(correlation_headers)\n"
    headers="${headers}$(arm_x_ms_identity_url_header)\n"
    echo -e "$headers" | curl --silent --header @- "${FRONTEND_HOST}${resource_id}?api-version=${FRONTEND_API_VERSION}"
}

rp_put_request() {
    # Arguments:
    # $1 = Resource ID
    # $2 = Request body (file path with @ or JSON string)
    # $3 = API version (optional, defaults to FRONTEND_API_VERSION)
    local resource_id=$1
    local request_body=$2
    local api_version=${3:-${FRONTEND_API_VERSION}}
    local headers=""
    headers="${headers}$(authorization_header)\n"
    headers="${headers}$(correlation_headers)\n"
    headers="${headers}$(arm_system_data_header)\n"
    headers="${headers}$(arm_x_ms_identity_url_header)\n"
    headers="${headers}$(header Content-Type "application/json")\n"

    echo -e "$headers" | curl --silent --header @- \
        --request PUT \
        --data "$request_body" \
        "${FRONTEND_HOST}${resource_id}?api-version=${api_version}"
}

rp_delete_request() {
    # Arguments:
    # $1 = Resource ID
    local resource_id=$1
    local headers=""
    headers="${headers}$(authorization_header)\n"
    headers="${headers}$(correlation_headers)\n"
    headers="${headers}$(arm_x_ms_identity_url_header)\n"

    echo -e "$headers" | curl --silent --header @- \
        --request DELETE \
        "${FRONTEND_HOST}${resource_id}?api-version=${FRONTEND_API_VERSION}"
}