#!/bin/bash
#
# Queries ACR for the latest image digest in a repository and writes
# a config override YAML file suitable for personal-dev-env deployments.
#
# Usage: record-latest-image-override.sh \
#          <acr-name> <registry> <repository> <deploy-env> <config-key> <output-file> <yq>

set -o errexit
set -o nounset
set -o pipefail

if [[ $# -ne 7 ]]; then
    echo "Usage: $0 <acr-name> <registry> <repository> <deploy-env> <config-key> <output-file> <yq>" >&2
    exit 1
fi

ACR_NAME=$1
REGISTRY=$2
REPOSITORY=$3
DEPLOY_ENV=$4
CONFIG_KEY=$5
OUTPUT_FILE=$6
YQ=$7

DIGEST=$(az acr manifest list-metadata \
    --registry "$ACR_NAME" \
    --name "$REPOSITORY" \
    --orderby time_desc --top 1 \
    --query '[0].digest' -o tsv)

if [[ -z "$DIGEST" ]]; then
    echo "Error: no images found for ${REPOSITORY} in ${ACR_NAME}" >&2
    exit 1
fi

$YQ eval -n "
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.${CONFIG_KEY}.image.registry = \"${REGISTRY}\" |
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.${CONFIG_KEY}.image.repository = \"${REPOSITORY}\" |
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.${CONFIG_KEY}.image.digest = \"${DIGEST}\"
" > "$OUTPUT_FILE"

echo "Using latest image for ${CONFIG_KEY}: ${REGISTRY}/${REPOSITORY}@${DIGEST}"
