#!/bin/bash
set -euo pipefail

# One-time migration script: sets uniqueName on existing Entra apps so that
# the Bicep Microsoft Graph extension can latch onto them instead of creating
# duplicates. Run this ONCE before the first mock-identity-apps.bicep deploy.
#
# The uniqueName is derived as toLower(replace(displayName, ' ', '-')) to match
# the convention in ci-bot-identity.bicep. mock-identity-apps.bicep must pass
# the same normalized value as uniqueName to entra/app.bicep.

if [ -n "${DRY_RUN:-}" ]; then
  echo "DRY_RUN mode enabled - will only show what would be patched, not actually patch anything"
  DRY_RUN_MODE=true
else
  DRY_RUN_MODE=false
fi

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

to_unique_name() {
  echo "$1" | tr '[:upper:]' '[:lower:]' | tr ' ' '-'
}

ERRORS=0
PATCHED=0
DENIED=0
for APP_NAME in "${APPS[@]}"; do
  UNIQUE_NAME=$(to_unique_name "${APP_NAME}")

  OBJECT_ID=$(az rest --method GET \
    --url "https://graph.microsoft.com/beta/applications?\$filter=displayName eq '${APP_NAME}'&\$select=id,uniqueName" \
    --query 'value[0].id' -o tsv 2>/dev/null || true)

  EXISTING=$(az rest --method GET \
    --url "https://graph.microsoft.com/beta/applications?\$filter=displayName eq '${APP_NAME}'&\$select=uniqueName" \
    --query 'value[0].uniqueName' -o tsv 2>/dev/null || true)

  if [[ -z "${OBJECT_ID}" || "${OBJECT_ID}" == "None" ]]; then
    echo "SKIP   ${APP_NAME} (not found)"
    continue
  fi

  if [[ -n "${EXISTING}" && "${EXISTING}" != "None" ]]; then
    if [[ "${EXISTING}" == "${UNIQUE_NAME}" ]]; then
      echo "OK     ${APP_NAME} -> ${UNIQUE_NAME}"
    else
      echo "ERROR  ${APP_NAME} (uniqueName='${EXISTING}' != expected '${UNIQUE_NAME}')"
      ERRORS=$((ERRORS + 1))
    fi
    continue
  fi

  if [ "$DRY_RUN_MODE" = true ]; then
    echo "NEEDS  ${APP_NAME} -> ${UNIQUE_NAME}"
  elif az rest --method PATCH \
    --url "https://graph.microsoft.com/beta/applications/${OBJECT_ID}" \
    --body "{\"uniqueName\": \"${UNIQUE_NAME}\"}" \
    -o none 2>/dev/null; then
    echo "DONE   ${APP_NAME} -> ${UNIQUE_NAME}"
    PATCHED=$((PATCHED + 1))
  else
    echo "DENIED ${APP_NAME} (no permission, needs owner)"
    DENIED=$((DENIED + 1))
  fi
done

echo ""
echo "patched=${PATCHED} denied=${DENIED} errors=${ERRORS}"
[[ ${ERRORS} -gt 0 ]] && exit 1
[[ ${DENIED} -gt 0 ]] && exit 2
exit 0
