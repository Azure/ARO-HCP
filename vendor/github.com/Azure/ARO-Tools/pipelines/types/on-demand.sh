#!/bin/bash

set -euo pipefail

retry() {
    local retries="${1}"
    shift
    local count=0
    until "$@"; do
        count=$((count + 1))
        if [[ "${count}" -ge "${retries}" ]]; then
            echo "Command failed after ${retries} attempts: $*" >&2
            return 1
        fi
        local delay=$((2 ** count))
        echo "Command failed (attempt ${count}/${retries}). Retrying in ${delay}s..." >&2
        sleep "${delay}"
    done
}

get_acr_domain_suffix() {
    local suffix
    if ! suffix="$(az cloud show --query "suffixes.acrLoginServerEndpoint" --output tsv)"; then
        return 1
    fi
    printf '%s' "${suffix}"
}

copyImageFromRegistry() {
    # shortcut mirroring if the source registry is the same as the target ACR
    REQUIRED_REGISTRY_VARS=("TARGET_ACR" "SOURCE_REGISTRY")
    for VAR in "${REQUIRED_REGISTRY_VARS[@]}"; do
        if [ -z "${!VAR}" ]; then
            echo "Error: Environment variable $VAR is not set."
            exit 1
        fi
    done
    ACR_DOMAIN_SUFFIX="$(retry 5 get_acr_domain_suffix)"
    if [[ "${SOURCE_REGISTRY}" == "${TARGET_ACR}${ACR_DOMAIN_SUFFIX}" ]]; then
        echo "Source and target registry are the same. No mirroring needed."
        exit 0
    fi

    # validate
    REQUIRED_VARS=("REPOSITORY" "DIGEST")
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

    if [[ -z "${PULL_SECRET_KV:-}" && -z "${PULL_SECRET:-}" ]]; then
        echo "No pull secret configured: pulling from public source registry."
    else
        # Check if source registry should use oc login , ENV variable is set in Release job.
        IS_CI_REGISTRY=false
        if [[ -n "${USE_OC_LOGIN_REGISTRIES:-}" ]]; then
            echo "Checking USE_OC_LOGIN_REGISTRIES: ${USE_OC_LOGIN_REGISTRIES}"
            for registry in ${USE_OC_LOGIN_REGISTRIES}; do
                if [[ "${SOURCE_REGISTRY}" == "${registry}" ]]; then
                    IS_CI_REGISTRY=true
                    break
                fi
            done
        fi

        if [[ "${IS_CI_REGISTRY}" == "true" ]]; then
            echo "Setting up registry authentication for CI source registry."
            retry 5 oc registry login --to "${AUTH_JSON}"
        else
            echo "Fetch pull secret for source registry ${SOURCE_REGISTRY} from ${PULL_SECRET_KV} KV."
            retry 5 az keyvault secret download \
                --vault-name "${PULL_SECRET_KV}" \
                --name "${PULL_SECRET}" \
                -e base64 \
                --file "${AUTH_JSON}"
        fi
    fi

    # ACR login to target registry
    echo "Logging into target ACR ${TARGET_ACR}."
    acr_login_target() {
      if output="$( az acr login --name "${TARGET_ACR}" --expose-token --only-show-errors --output json 2>&1 )"; then
        RESPONSE="${output}"
      else
        echo "Failed to log in to ACR ${TARGET_ACR}: ${output}" >&2
        return 1
      fi
    }
    retry 5 acr_login_target
    TARGET_ACR_LOGIN_SERVER="$(jq --raw-output .loginServer <<<"${RESPONSE}" )"
    ACCESS_TOKEN="$(jq --raw-output .accessToken <<<"${RESPONSE}")"
    oras_login_target() {
      oras login --registry-config "${AUTH_JSON}" \
                 --username 00000000-0000-0000-0000-000000000000 \
                 --password-stdin \
                 "${TARGET_ACR_LOGIN_SERVER}" <<<"${ACCESS_TOKEN}"
    }
    retry 5 oras_login_target

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
    retry 5 oras cp "${SRC_IMAGE}" "${TARGET_IMAGE}" \
        --from-registry-config "${AUTH_JSON}" \
        --to-registry-config "${AUTH_JSON}"
}

copyImageFromOciLayout() {
    # validate required variables
    REQUIRED_VARS=("TARGET_ACR" "REPOSITORY" "IMAGE_TAR_FILE_NAME" "IMAGE_METADATA_FILE_NAME")
    for VAR in "${REQUIRED_VARS[@]}"; do
        if [ -z "${!VAR}" ]; then
            echo "Error: Environment variable $VAR is not set."
            exit 1
        fi
    done

    # set image file path to pwd if not set
    if [ -z "${IMAGE_FILE_PATH}" ]; then
        IMAGE_FILE_PATH="$(pwd)"
    fi

    IMAGE_TAR_FILE="${IMAGE_FILE_PATH}/${IMAGE_TAR_FILE_NAME}"
    # validate image tar file exists
    if [ ! -f "${IMAGE_TAR_FILE}" ]; then
        echo "Error: Image tar file ${IMAGE_TAR_FILE_NAME} does not exist at a given path ${IMAGE_FILE_PATH}."
        exit 1
    fi

    IMAGE_METADATA_FILE="${IMAGE_FILE_PATH}/${IMAGE_METADATA_FILE_NAME}"
    # validate image metadata file exists
    if [ ! -f "${IMAGE_METADATA_FILE}" ]; then
        echo "Error: Image metadata file ${IMAGE_METADATA_FILE_NAME} does not exist at a given path ${IMAGE_FILE_PATH}."
        exit 1
    fi

    # Extract build_tag using jq
    BUILD_TAG=$(jq -r '.build_tag' "$IMAGE_METADATA_FILE")

    if [[ -z "$BUILD_TAG" || "$BUILD_TAG" == "null" ]]; then
    echo "❌ build_tag not found in $IMAGE_METADATA_FILE" >&2
    exit 1
    fi

    echo "✅ build_tag is: $BUILD_TAG"

    ACR_DOMAIN_SUFFIX="$(retry 5 get_acr_domain_suffix)"
    TARGET_ACR_LOGIN_SERVER="${TARGET_ACR}${ACR_DOMAIN_SUFFIX}"

    echo "Getting the ACR access token."
    USERNAME="00000000-0000-0000-0000-000000000000"
    acr_login_oci() {
      if ! PASSWORD=$(az acr login --name "$TARGET_ACR" --expose-token --only-show-errors --output tsv --query accessToken); then
        echo "Failed to get ACR access token for ${TARGET_ACR}" >&2
        return 1
      fi
    }
    retry 5 acr_login_oci

    echo "Logging in with ORAS."
    oras_login_oci() {
      oras login "$TARGET_ACR_LOGIN_SERVER" --username "$USERNAME" --password-stdin <<< "$PASSWORD"
    }
    retry 5 oras_login_oci

    # Check for DRY_RUN
    if [ "${DRY_RUN:-false}" == "true" ]; then
        echo "DRY_RUN is enabled. Exiting without making changes."
        exit 0
    fi

    # copy image from OCI layout to ACR
    TARGET_IMAGE="${TARGET_ACR_LOGIN_SERVER}/${REPOSITORY}:${BUILD_TAG}"
    retry 5 oras cp \
        --from-oci-layout "${IMAGE_TAR_FILE}:${BUILD_TAG}" \
        "${TARGET_IMAGE}"
}

if [[ -z "${IMAGE_TAR_FILE_NAME:-}" ]]; then
    copyImageFromRegistry
else
    copyImageFromOciLayout
fi
