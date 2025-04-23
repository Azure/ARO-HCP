#!/bin/bash

set -euxo pipefail

echo "Azure login"
az login --identity --client-id "${AZURE_CLIENT_ID}"

echo "ACR login"
DOCKER_COMMAND=/usr/local/bin/docker-login.sh az acr login -n "${REGISTRY}"

# Prepare configuration
IMAGE_SET_CONFIG_FILE="/config/imageset-config.yaml"
echo "${IMAGE_SET_CONFIG}" | base64 -d | yq eval -P > ${IMAGE_SET_CONFIG_FILE}
API_VERSION=$(yq eval '.apiVersion' ${IMAGE_SET_CONFIG_FILE})
if echo "$API_VERSION" | grep -q "^mirror.openshift.io/v2"; then
    ADDITIONAL_FLAGS="--workspace file:///oc-mirror-workspace --v2"
fi

# switching between versions of oc-mirror is a temporary fix until
# all oc-mirror related problems have been resolved
# * https://issues.redhat.com/browse/OCPBUGS-54340 - storage issue
# * https://issues.redhat.com/browse/CLID-325 - CPU bug
# * https://issues.redhat.com/browse/OCPBUGS-52471 - memory bug
if [ "$OC_MIRROR_COMPATIBILITY" = "NOCATALOG" ]; then
    export OC_MIRROR_VERSION="4.16"
else
    export OC_MIRROR_VERSION="4.18"
fi
echo "Using oc-mirror version: ${OC_MIRROR_VERSION}"

echo "Inspecting DNS for target registry"
dig "${REGISTRY_URL}"

echo "Start mirroring"
/usr/local/bin/oc-mirror-${OC_MIRROR_VERSION} --config ${IMAGE_SET_CONFIG_FILE} ${ADDITIONAL_FLAGS} docker://${REGISTRY_URL} @$
