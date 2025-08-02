#!/bin/bash
#
# Outputs an image digest from Azure Container Registry.
#
# By default, this selects the first non-test image (based on the tag name)
# from the specified Azure container registry and repository.
#
# But there are a couple ways to override this:
#
# 1) Specify the image digest directly with IMAGE_DIGEST.
#
# 2) Specify an image tag to match with IMAGE_TAG. If the image tag is not
#    matched, fallback to the default selection (with a warning message).
#

set -o errexit
set -o nounset
set -o pipefail

if [ -v IMAGE_DIGEST ] && [ -n "${IMAGE_DIGEST}" ]
then
    echo ${IMAGE_DIGEST}
    exit 0
fi

if [ "$#" -ne 2 ]
then
    echo "Need ARO_HCP_IMAGE_ACR and REPOSITORY parameters"
    exit 1
fi

aro_hcp_image_acr=${1}
repository=${2}

tags=$(mktemp)
trap "rm ${tags}" EXIT

az acr repository show-tags --orderby time_desc --n ${aro_hcp_image_acr} --repository ${repository} --detail --output json > $tags

if [ -v IMAGE_TAG ] && [ -n "${IMAGE_TAG}" ]
then
    suggested_digest=$(jq -r --arg TAG ${IMAGE_TAG} 'first(.[] | select(.name==$TAG) | .digest)' $tags)
    if [ -n "${suggested_digest}" ]
    then
        echo ${suggested_digest}
        exit 0
    fi
    echo "Image tag ${IMAGE_TAG} not found, using a fallback image" > /dev/stderr
fi

# Exclude test images for personal dev environments.
jq -r 'first(.[] | select(.name | startswith("test-") | not) | .digest)' $tags
