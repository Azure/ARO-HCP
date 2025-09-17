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


# Start the Prow job
./prow-job-executor execute --prow-token-keyvault-uri "https://${PROW_TOKEN_KEYVAULT}.${KEY_VAULT_DNSSUFFIX}" --prow-token-keyvault-secret "$PROW_TOKEN_SECRET" --job-name "$PROW_JOB_NAME" --region "$REGION" --ev2-rollout-version "${zz_injected_EV2RolloutVersion:-}"
