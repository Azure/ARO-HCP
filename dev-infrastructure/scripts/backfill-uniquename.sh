#!/bin/bash
set -euo pipefail

# One-time migration script: sets uniqueName on existing Entra apps so that
# the Bicep Microsoft Graph extension can latch onto them instead of creating
# duplicates. Run this ONCE before the first mock-identity-apps.bicep deploy.
#
# The uniqueName values match what entra/app.bicep will use: it defaults
# uniqueName to applicationName, which is exactly what mock-identity-apps.bicep
# passes.

APPS=(
  # DEV shared identities
  "aro-dev-first-party2"
  "aro-dev-arm-helper2"
  "aro-dev-msi-mock2"
  # INT shared identities
  "aro-hcp-int-fp"
  "aro-hcp-int-arm-helper"
  "aro-hcp-int-msi-mock"
)

DEV_POOL_SIZE="${DEV_POOL_SIZE:-20}"
DEV_POOL_BASE="aro-dev-msi-mock-pool"

for i in $(seq 0 $((DEV_POOL_SIZE - 1))); do
  APPS+=("${DEV_POOL_BASE}-${i}")
done

ERRORS=0
for APP_NAME in "${APPS[@]}"; do
  OBJECT_ID=$(az rest --method GET \
    --url "https://graph.microsoft.com/beta/applications?\$filter=displayName eq '${APP_NAME}'&\$select=id,uniqueName" \
    --query 'value[0].id' -o tsv 2>/dev/null || true)

  if [[ -z "${OBJECT_ID}" || "${OBJECT_ID}" == "None" ]]; then
    echo "SKIP  ${APP_NAME} (not found)"
    continue
  fi

  EXISTING=$(az rest --method GET \
    --url "https://graph.microsoft.com/beta/applications?\$filter=displayName eq '${APP_NAME}'&\$select=uniqueName" \
    --query 'value[0].uniqueName' -o tsv 2>/dev/null || true)

  if [[ -n "${EXISTING}" && "${EXISTING}" != "None" ]]; then
    if [[ "${EXISTING}" == "${APP_NAME}" ]]; then
      echo "OK    ${APP_NAME} (uniqueName already set correctly)"
    else
      echo "ERROR ${APP_NAME} (uniqueName='${EXISTING}' != expected '${APP_NAME}')"
      ERRORS=$((ERRORS + 1))
    fi
    continue
  fi

  echo -n "PATCH ${APP_NAME} ... "
  az rest --method PATCH \
    --url "https://graph.microsoft.com/beta/applications/${OBJECT_ID}" \
    --body "{\"uniqueName\": \"${APP_NAME}\"}" \
    -o none
  echo "done"
done

if [[ ${ERRORS} -gt 0 ]]; then
  echo "WARNING: ${ERRORS} app(s) have mismatched uniqueName values"
  exit 1
fi

echo "Backfill complete."
