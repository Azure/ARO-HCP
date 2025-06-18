#!/bin/bash

USERNAME=$3
PASSWORD=$5

PLAIN_CREDS="$USERNAME:$PASSWORD"
AUTH=$(echo -n $PLAIN_CREDS | base64)

jq --arg registry "$REGISTRY_URL" --arg auth "$AUTH" '.auths[$registry] = { "auth": $auth }' ${XDG_RUNTIME_DIR}/containers/auth.json > ${XDG_RUNTIME_DIR}/containers/tmp-auth.json
cp ${XDG_RUNTIME_DIR}/containers/tmp-auth.json ${XDG_RUNTIME_DIR}/containers/auth.json
