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

if [ "$OC_MIRROR_COMPATIBILITY" = "NOCATALOG" ]; then
    export OC_MIRROR_VERSION="4.16"
else
    export OC_MIRROR_VERSION="4.18"
fi
echo "Using oc-mirror version: ${OC_MIRROR_VERSION}"

/usr/local/bin/oc-mirror-${OC_MIRROR_VERSION} --config ${IMAGE_SET_CONFIG_FILE} ${ADDITIONAL_FLAGS} docker://${REGISTRY_URL} @$
