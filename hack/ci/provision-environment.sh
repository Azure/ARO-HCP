#!/bin/bash
# Shared provisioning logic for ARO HCP CI environments.
# Called by step-registry wrappers: aro-hcp-provision-environment,
# aro-hcp-provision-from-main, and aro-hcp-hypershift-deploy.
# Do not run directly.
set -o errexit
set -o nounset
set -o pipefail

: "${SHARED_DIR:?SHARED_DIR must be set}"
: "${ARTIFACT_DIR:?ARTIFACT_DIR must be set}"
: "${LOCATION:?LOCATION must be set}"

# shellcheck source=hack/ci/az-login.sh
source "$(dirname "$0")/az-login.sh"
oc version
kubelogin --version

# Build config override (image overrides, MSI mock SP, MGMT sizing, hypershift merges)
# shellcheck source=hack/ci/build-config-override.sh
source "$(dirname "$0")/build-config-override.sh"

# --- Provision ---

CONFIG_PROV="${SHARED_DIR}/config-prov.yaml"

# There's a $SHARED_DIR/config.yaml already from the write-config step
# but it is of limited accuracy. It's fine for int/stg/prod, but this prov
# step will generate temporary names for a bunch of things, so if we want
# following steps to know what those are, we need to override the older
# less accurate config.yaml.
# And let's do it in a way that works even if provisioning ends up failing.
finalize() {
    if [[ -s "${CONFIG_PROV}" ]]; then
        mv "${CONFIG_PROV}" "${SHARED_DIR}/config.yaml"
        cp "${SHARED_DIR}/config.yaml" "${ARTIFACT_DIR}/config.yaml"
    fi
}
trap finalize EXIT

unset GOFLAGS

EXTRA_ARGS="--region ${LOCATION}"
if [[ "${ARO_HCP_PROVISION_ABORT_IF_EXISTS:-true}" == "true" ]]; then
  EXTRA_ARGS+=" --abort-if-regional-exist"
fi
# Run cost attribution: jobs opt in by setting RUN_COST_TAG_VALUE in the
# openshift/release job configuration. The literal BUILD_ID resolves to this
# job's Prow build ID, because ci-operator configuration cannot expand
# $(BUILD_ID) itself. The tag key matches the config override generator's
# RunCostTagKey; templatize sets it on resource groups this run creates and
# only replaces it on existing groups that already carry it.
if [[ -n "${RUN_COST_TAG_VALUE:-}" ]]; then
  if [[ "${RUN_COST_TAG_VALUE}" == "BUILD_ID" ]]; then
    : "${BUILD_ID:?BUILD_ID must be set when RUN_COST_TAG_VALUE is BUILD_ID}"
    RUN_COST_TAG_VALUE="${BUILD_ID}"
  fi
  EXTRA_ARGS+=" --resource-group-tags=jobID.aro-hcp-ci.redhat.com=${RUN_COST_TAG_VALUE}"
fi

STEP_NAME="${PROVISION_STEP_NAME:-entrypoint}"
STEP_NAME="${STEP_NAME//[^a-zA-Z0-9_-]/}"
: "${STEP_NAME:=entrypoint}"
make -o "tooling/templatize/templatize-$(uname -m)" entrypoint/Region \
  DEPLOY_ENV="${DEPLOY_ENV}" \
  OVERRIDE_CONFIG_FILE="${OVERRIDE_CONFIG_FILE}" \
  EXTRA_ARGS="${EXTRA_ARGS}" \
  TIMING_OUTPUT=${SHARED_DIR}/steps.yaml.gz \
  ENTRYPOINT_JUNIT_OUTPUT=${ARTIFACT_DIR}/junit_${STEP_NAME}.xml \
  CONFIG_OUTPUT=${CONFIG_PROV}

MARKER="${PROVISION_COMPLETE_MARKER:-provision-complete}"
MARKER="${MARKER//[^a-zA-Z0-9_-]/}"
: "${MARKER:=provision-complete}"
touch "${SHARED_DIR}/${MARKER}"
