#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

function waitBeforeExit() {
  local exit_code=$?
  echo "Waiting before exiting to ensure that logs are captured by Azure Container Instance."
  sleep 10
  exit "$exit_code"
}

trap waitBeforeExit EXIT

if [ -z "${PROW_JOB_NAME:-}" ]; then
  echo "PROW_JOB_NAME is not set. Exiting."
  exit 0
fi

BASE_SHA_ARGS=()
if [ -n "${BASE_SHA:-}" ]; then
  BASE_SHA_ARGS=(--base-sha "$BASE_SHA")
fi

# When running via EV2, verify the current commit matches the ARO-HCP SHA
# encoded in the rollout version (e.g. build.156499306.sdp-pipelines.e5ca8e1b5.ARO-HCP.51f51a32c97c)
if [ -n "${zz_injected_EV2RolloutVersion:-}" ]; then
  rollout_sha="${zz_injected_EV2RolloutVersion##*.}"
  current_sha="$(git rev-parse --short=12 HEAD)"
  if [ "$rollout_sha" != "$current_sha" ]; then
    echo "ERROR: ARO-HCP commit mismatch. EV2 rollout version contains '$rollout_sha' but current HEAD is '$current_sha'."
    exit 1
  fi
fi

"${PROW_JOB_EXECUTOR}" execute --prow-token-keyvault-uri "https://${PROW_TOKEN_KEYVAULT}.${KEY_VAULT_DNSSUFFIX}" --prow-token-keyvault-secret "$PROW_TOKEN_SECRET" --job-name "$PROW_JOB_NAME" --cloud "$CLOUD" --environment "$ENVIRONMENT" --region "$REGION" --ev2-rollout-version "${zz_injected_EV2RolloutVersion:-}" --dry-run="${DRY_RUN:-false}" --gate-promotion="${GATE_PROMOTION:-false}" ${BASE_SHA_ARGS[@]+"${BASE_SHA_ARGS[@]}"}
