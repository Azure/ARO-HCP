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
ADDITIONAL_FLAGS=""
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
MIRROR_LOG=$(mktemp)
trap 'rm -f "${MIRROR_LOG}"' EXIT
/usr/local/bin/oc-mirror-${OC_MIRROR_VERSION} --config ${IMAGE_SET_CONFIG_FILE} ${ADDITIONAL_FLAGS} docker://${REGISTRY_URL} "$@" 2>&1 | tee "${MIRROR_LOG}"

# oc-mirror v2 exits 0 even when every image fails to mirror, so a broken job
# reports success. Inspect its summary line and fail when nothing was mirrored.
# The summary is prefixed with a timestamp and log level, e.g.
#   2026/08/12 23:00:35  [INFO]   :    0 / 4 additional images mirrored: ...
# Requiring whitespace before the 0 avoids matching counts such as "10 / 20".
if grep -qE '[[:space:]]0 / [1-9][0-9]* (additional|release|operator) images mirrored' "${MIRROR_LOG}"; then
    echo "ERROR: oc-mirror reported 0 mirrored images" >&2
    exit 1
fi
