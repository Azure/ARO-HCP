#!/bin/bash
# Provision an ARO HCP environment with ACR-resolved main-branch images.
# Used by the hypershift PR testing workflow to deploy a full environment
# with current main service images plus PR-built HO/CPO image overrides.
#
# Called by the aro-hcp-hypershift-deploy step-registry wrapper.
# Named provision-hypershift.sh to follow the provision-* convention.
# Callers must set: CLUSTER_PROFILE_DIR, ARO_HCP_DEPLOY_ENV, SHARED_DIR,
#                   ARTIFACT_DIR, LOCATION
set -o errexit
set -o nounset
set -o pipefail

: "${CLUSTER_PROFILE_DIR:?CLUSTER_PROFILE_DIR must be set}"
: "${ARO_HCP_DEPLOY_ENV:?ARO_HCP_DEPLOY_ENV must be set}"

# --- Resolve ARO-HCP service images from dev ACR by main commit SHA ---
# The images-push-postsubmit job publishes service images on every merge
# to ARO-HCP main, tagged with the 7-char commit SHA. We resolve images
# by SHA to guarantee version coherence across all services.

export AZURE_CLIENT_ID; AZURE_CLIENT_ID=$(cat "${CLUSTER_PROFILE_DIR}/client-id")
export AZURE_TENANT_ID; AZURE_TENANT_ID=$(cat "${CLUSTER_PROFILE_DIR}/tenant")
export AZURE_CLIENT_SECRET; AZURE_CLIENT_SECRET=$(cat "${CLUSTER_PROFILE_DIR}/client-secret")

set +o xtrace
az login --service-principal \
  -u "${AZURE_CLIENT_ID}" \
  -p "${AZURE_CLIENT_SECRET}" \
  --tenant "${AZURE_TENANT_ID}" \
  --output none
set -o xtrace

export ACR_CONFIG_FILE="${SHARED_DIR}/config.yaml"

git fetch https://github.com/Azure/ARO-HCP.git main
export TARGET_SHA
TARGET_SHA=$(git rev-parse --short=7 FETCH_HEAD)
echo "ARO-HCP main HEAD: ${TARGET_SHA}"

# Resolve service images from ACR
# shellcheck source=hack/ci/resolve-acr-images.sh
source "$(dirname "$0")/resolve-acr-images.sh"

exec "$(dirname "$0")/provision-environment.sh"
