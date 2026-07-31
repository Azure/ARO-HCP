#!/bin/bash
# Provision a baseline ARO HCP environment from the base branch.
# Used as the first phase of upgrade-path validation: the environment
# is created from main, then upgraded to the PR branch in a subsequent step.
#
# Called by the aro-hcp-provision-from-main step-registry wrapper.
# Callers must set: CLUSTER_PROFILE_DIR, ARO_HCP_DEPLOY_ENV, SHARED_DIR,
#                   ARTIFACT_DIR, LOCATION
set -o errexit
set -o nounset
set -o pipefail

# shellcheck source=hack/ci/az-login.sh
source "$(dirname "$0")/az-login.sh"

# Check out the base branch to provision the baseline environment.
# The container has the PR merge commit baked in; we rewind to the base
# so Bicep templates, Helm charts, config, and pipeline definitions all
# come from what the PR is being merged into.
#
# Prow sets PULL_BASE_SHA to the exact base-branch commit used for the
# merge. That commit is already in the local clone, so no fetch needed.
# Rehearsal runs (JOB_NAME prefixed with "rehearse-") fetch main
# explicitly, since PULL_BASE_SHA belongs to the openshift/release repo.
IS_REHEARSAL=false
if [[ "${JOB_NAME:-}" == rehearse-* ]]; then
  IS_REHEARSAL=true
fi

if [[ "${IS_REHEARSAL}" == "true" ]]; then
  echo "Rehearsal detected (JOB_NAME=${JOB_NAME:-unset}), fetching main ..."
  git fetch https://github.com/Azure/ARO-HCP.git main
  git checkout -f FETCH_HEAD
else
  if [[ -z "${PULL_BASE_SHA:-}" ]]; then
    echo "ERROR: PULL_BASE_SHA is not set and this is not a rehearsal. Cannot determine base commit."
    exit 1
  fi
  if ! git cat-file -e "${PULL_BASE_SHA}" 2>/dev/null; then
    echo "ERROR: PULL_BASE_SHA=${PULL_BASE_SHA} not found in git history."
    exit 1
  fi
  echo "Using Prow merge base ${PULL_BASE_SHA}"
  git checkout -f "${PULL_BASE_SHA}"
fi
echo "Checked out base at $(git rev-parse --short HEAD)"

# The images-push-postsubmit job runs the aro-hcp-images-push step on every
# merge to main (DEPLOY_ENV=dev), mirroring CI-built service images into the
# shared SVC ACR tagged with the 7-char commit SHA. We resolve ACR/repo
# coordinates from the dev config to match, since that's what images-push uses.
export TARGET_SHA
TARGET_SHA=$(git rev-parse --short=7 HEAD)

IMAGES_DEPLOY_ENV="dev"
export ACR_CONFIG_FILE="config/rendered/dev/${IMAGES_DEPLOY_ENV}/westus3.yaml"
if [[ ! -f "${ACR_CONFIG_FILE}" ]]; then
  ACR_CONFIG_FILE="config/rendered/dev/${IMAGES_DEPLOY_ENV}/centralus.yaml"
fi
if [[ ! -f "${ACR_CONFIG_FILE}" ]]; then
  echo "ERROR: No rendered config found for ${IMAGES_DEPLOY_ENV} (tried westus3.yaml, centralus.yaml)"
  exit 1
fi

# Resolve service images from ACR
# shellcheck source=hack/ci/resolve-acr-images.sh
source "$(dirname "$0")/resolve-acr-images.sh"

export PROVISION_STEP_NAME=entrypoint_baseline
export PROVISION_COMPLETE_MARKER=provision-from-main-complete

exec "$(dirname "$0")/provision-environment.sh"
