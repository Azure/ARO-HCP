#!/bin/bash

set -euo pipefail

# only run within EV2 for now
# RH dev envs have the regular component sync in place to make images available
if [[ -z "${EV2:-}" ]]; then
    echo "The image-sync/on-demand/sync.sh script runs only within EV2. Exiting."
    exit 0
fi

# validate
REQUIRED_VARS=("PULL_SECRET_KV" "PULL_SECRET" "TARGET_ACR" "SOURCE_REGISTRY" "REPOSITORY" "DIGEST")
for VAR in "${REQUIRED_VARS[@]}"; do
    if [ -z "${!VAR}" ]; then
        echo "Error: Environment variable $VAR is not set."
        exit 1
    fi
done

# create temporary FS structure
TMP_DIR="$(mktemp -d)"
CONTAINERS_DIR="${TMP_DIR}/containers"
AUTH_JSON="${CONTAINERS_DIR}/auth.json"
ORAS_CACHE="${TMP_DIR}/oras-cache"
mkdir -p "${CONTAINERS_DIR}"
mkdir -p "${ORAS_CACHE}"
trap 'rm -rf ${TMP_DIR}' EXIT

# get pull secret for source registry
echo "Fetch pull secret for source registry ${SOURCE_REGISTRY} from ${PULL_SECRET_KV} KV."
az keyvault secret download --vault-name "${PULL_SECRET_KV}" --name "${PULL_SECRET}" -e base64 --file "${AUTH_JSON}"

# ACR login to target registry
echo "Logging into target ACR ${TARGET_ACR}."
if output="$( az acr login --name "${TARGET_ACR}" --expose-token --only-show-errors 2>&1 )"; then
  RESPONSE="${output}"
else
  echo "Failed to log in to ACR ${TARGET_ACR}: ${output}"
  exit 1
fi
TARGET_ACR_LOGIN_SERVER="$(jq --raw-output .loginServer <<<"${RESPONSE}" )"
oras login --registry-config "${AUTH_JSON}" \
           --username 00000000-0000-0000-0000-000000000000 \
           --password-stdin \
           "${TARGET_ACR_LOGIN_SERVER}" <<<"$( jq --raw-output .accessToken <<<"${RESPONSE}" )"

# at this point we have an auth config that can read from the source registry and
# write to the target registry.

# Check for DRY_RUN
if [ "${DRY_RUN:-false}" == "true" ]; then
    echo "DRY_RUN is enabled. Exiting without making changes."
    exit 0
fi

# mirror image
SRC_IMAGE="${SOURCE_REGISTRY}/${REPOSITORY}@${DIGEST}"
DIGEST_NO_PREFIX=${DIGEST#sha256:}
# we use the digest as a tag so the image can be inspected easily in the ACR
# this does not affect the fact that the image is stored by immutable digest in the ACR
# it is crucial though, that the tagged image is not used in favor of the @sha256:digest one
# as the tag is NOT guaranteed to be immutable
TARGET_IMAGE="${TARGET_ACR_LOGIN_SERVER}/${REPOSITORY}:${DIGEST_NO_PREFIX}"
echo "Mirroring image ${SRC_IMAGE} to ${TARGET_IMAGE}."
echo "The image will still be available under it's original digest ${DIGEST} in the target registry."
oras cp "${SRC_IMAGE}" "${TARGET_IMAGE}" --from-registry-config "${AUTH_JSON}" --to-registry-config "${AUTH_JSON}"
