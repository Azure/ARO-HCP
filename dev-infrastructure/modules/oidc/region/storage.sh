#!/bin/bash
set -euo pipefail

MAX_RETRIES=15
RETRY_INTERVAL=10

echo "Enable static Website on storage account ${StorageAccountName}"
for i in $(seq 1 ${MAX_RETRIES}); do
  if az storage blob service-properties update --static-website true --account-name "${StorageAccountName}" --auth-mode login; then
    echo "Static website enabled successfully"
    exit 0
  fi
  echo "Attempt ${i}/${MAX_RETRIES} failed, retrying in ${RETRY_INTERVAL}s (waiting for RBAC propagation)..."
  sleep ${RETRY_INTERVAL}
done

echo "Failed to enable static website after ${MAX_RETRIES} attempts"
exit 1
