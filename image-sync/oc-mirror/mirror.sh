#!/bin/sh
az login --identity -u ${AZURE_CLIENT_ID}
DOCKER_COMMAND=/usr/local/bin/docker-login.sh az acr login -n ${REGISTRY}

IMAGE_SET_CONFIG_FILE="/config/imageset-config.yaml"
echo ${IMAGE_SET_CONFIG} | base64 -d | yq eval -P > ${IMAGE_SET_CONFIG_FILE}
API_VERSION=$(yq eval '.apiVersion' ${IMAGE_SET_CONFIG_FILE})

if echo "$API_VERSION" | grep -q "^mirror.openshift.io/v2"; then
    ADDITIONAL_FLAGS="--workspace file:///oc-mirror-workspace --v2"
else
    ADDITIONAL_FLAGS="--continue-on-error"
fi

/usr/local/bin/oc-mirror --config ${IMAGE_SET_CONFIG_FILE} ${ADDITIONAL_FLAGS} docker://${REGISTRY_URL} @$
