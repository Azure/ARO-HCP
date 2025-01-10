#!/bin/bash

arm_system_data_header() {
    echo "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"${USER}\", \"createdByType\": \"User\", \"createdAt\": \"$(date -u +"%Y-%m-%dT%H:%M:%S+00:00")\"}"
}

correlation_headers() {
    local HEADERS=( )
    if [ -n "$(which uuidgen 2> /dev/null)" ]; then
        HEADERS+=( "X-Ms-Correlation-Request-Id: $(uuidgen)" )
        HEADERS+=( "X-Ms-Client-Request-Id: $(uuidgen)" )
        HEADERS+=( "X-Ms-Return-Client-Request-Id: true" )
    fi
    printf '%s\n' "${HEADERS[@]}"
}
