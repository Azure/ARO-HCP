#!/bin/sh
az login --identity -u ${AZURE_CLIENT_ID}
echo ${IMAGE_SET_CONFIG} | base64 -d | yq eval -P > /config/imageset-config.yaml
DOCKER_COMMAND=/usr/local/bin/docker-login.sh az acr login -n ${REGISTRY}
/usr/local/bin/oc-mirror --continue-on-error --config /config/imageset-config.yaml docker://${REGISTRY_URL} @$
