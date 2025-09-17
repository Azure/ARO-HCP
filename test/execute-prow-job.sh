#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Stop execution if dry run is enabled
if [ "${DRY_RUN:-false}" = "true" ]; then
  echo "DRY_RUN is enabled. Stopping execution before running PROW job."
  exit 0
fi

if [ -z "${PROW_JOB_NAME:-}" ]; then
  echo "PROW_JOB_NAME is not set. Exiting."
  exit 0
fi

echo "Starting Prow job: ${PROW_JOB_NAME}"
set +o errexit
./prow-job-executor execute --prow-token-keyvault-uri "https://${PROW_TOKEN_KEYVAULT}.${KEY_VAULT_DNSSUFFIX}" --prow-token-keyvault-secret "$PROW_TOKEN_SECRET" --job-name "$PROW_JOB_NAME" --region "$REGION" --ev2-rollout-version "${zz_injected_EV2RolloutVersion:-}"
JOB_EXIT_CODE=$?
set -o errexit

# Only fail the script if GATE_PROMOTION is set to true and the job failed
if [ "${GATE_PROMOTION:-false}" = "true" ] && [ $JOB_EXIT_CODE -ne 0 ]; then
  echo "GATE_PROMOTION is enabled and Prow job failed with exit code $JOB_EXIT_CODE. Failing the test."
  exit $JOB_EXIT_CODE
elif [ $JOB_EXIT_CODE -ne 0 ]; then
  echo "Prow job failed with exit code $JOB_EXIT_CODE, but GATE_PROMOTION is not enabled. Continuing..."
else
  echo "Prow job completed successfully."
fi
