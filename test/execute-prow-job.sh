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

"${PROW_JOB_EXECUTOR}" execute --prow-token-keyvault-uri "https://${PROW_TOKEN_KEYVAULT}.${KEY_VAULT_DNSSUFFIX}" --prow-token-keyvault-secret "$PROW_TOKEN_SECRET" --job-name "$PROW_JOB_NAME" --region "$REGION" --ev2-rollout-version "${zz_injected_EV2RolloutVersion:-}" --dry-run="${DRY_RUN:-false}" --gate-promotion="${GATE_PROMOTION:-false}"
