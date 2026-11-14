#!/bin/bash
# Provision an ARO HCP environment from a published main-branch revision.
# Used for upgrade baselines and periodic regional provisioning healthchecks.
#
# Called by the aro-hcp-provision-from-main step-registry wrapper.
# Callers must set: CLUSTER_PROFILE_DIR, ARO_HCP_DEPLOY_ENV, SHARED_DIR,
#                   ARTIFACT_DIR, LOCATION
set -o errexit
set -o nounset
set -o pipefail

# shellcheck source=hack/ci/az-login.sh
source "$(dirname "$0")/az-login.sh"

# Check out the main-branch revision to provision.
# Presubmits rewind the PR merge commit to its base, while periodics and
# rehearsals fetch main because they do not have an ARO-HCP PULL_BASE_SHA.
#
# Prow sets PULL_BASE_SHA to the exact base-branch commit used for a
# presubmit merge. That commit is already in the local clone, so no fetch
# is needed. Periodic and rehearsal runs fetch main explicitly.
#
# TEMPORARY: pin baseline to main immediately before #7894 (component image
# digest bump) so inplace-upgrade provisions from that revision instead of
# tip-of-main / PULL_BASE_SHA.
BASELINE_SHA="6dcc32ab0a771cb5088b345998eea1bceedb2ea7"
echo "TEMPORARY: forcing baseline checkout to ${BASELINE_SHA} (pre-#7894)"
git fetch https://github.com/Azure/ARO-HCP.git "${BASELINE_SHA}"
git checkout -f "${BASELINE_SHA}"
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
