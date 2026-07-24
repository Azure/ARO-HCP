#!/bin/bash
set -euo pipefail

# One-time pre-merge script: deletes the legacy role assignments created by
# grant-openshift-release-bot-dev.sh so that ci-bot-rbac-dev can recreate
# them with deterministic guid()-based names.
#
# Requires Owner or User Access Administrator on each subscription.

if [ -n "${APPLY:-}" ]; then
  DRY_RUN_MODE=false
else
  echo "DRY_RUN mode (default) - will only show what would be deleted. Set APPLY=1 to delete."
  DRY_RUN_MODE=true
fi

az account show -o none 2>/dev/null || { echo "ERROR: not logged in to Azure"; exit 1; }

SP_COUNT=$(az ad sp list --display-name "OpenShift Release Bot" --query 'length(@)' -o tsv 2>/dev/null)
if [[ "${SP_COUNT}" -ne 1 ]]; then
  echo "ERROR: expected 1 'OpenShift Release Bot' SP, found ${SP_COUNT}"
  exit 1
fi

BOT_SP_ID=$(az ad sp list --display-name "OpenShift Release Bot" --query '[0].id' -o tsv 2>/dev/null)
echo "Bot SP objectId: ${BOT_SP_ID}"

SUBSCRIPTIONS=(
  "1d3378d3-5a3f-4712-85a1-2485495dfc4b"
  "974ebd46-8ad3-41e3-afef-7ef25fd5c371"
  "e8c5a115-842d-4d7e-98ad-cfb2c50b209e"
  "0ef1ad54-9296-44cd-9600-5dc8e9a74034"
  "e627aa70-36a3-40b0-8e68-975269e39d7b"
  "6ed122d1-7e03-4a01-baae-9020abf350d4"
)

DELETED=0
ERRORS=0
for SUB_ID in "${SUBSCRIPTIONS[@]}"; do
  ASSIGNMENTS=$(az role assignment list \
    --assignee "${BOT_SP_ID}" \
    --subscription "${SUB_ID}" \
    --query '[].id' -o tsv 2>/dev/null) || {
    echo "SKIP   ${SUB_ID} (no access)"
    continue
  }

  if [[ -z "${ASSIGNMENTS}" ]]; then
    echo "NONE   ${SUB_ID}"
    continue
  fi

  while IFS= read -r ASSIGNMENT_ID; do
    if [ "$DRY_RUN_MODE" = true ]; then
      ROLE=$(az role assignment list --subscription "${SUB_ID}" \
        --query "[?id=='${ASSIGNMENT_ID}'].roleDefinitionName | [0]" -o tsv 2>/dev/null)
      echo "WOULD  ${SUB_ID} ${ROLE}"
    elif az role assignment delete --ids "${ASSIGNMENT_ID}" 2>/dev/null; then
      echo "DONE   ${SUB_ID} ${ASSIGNMENT_ID##*/}"
      DELETED=$((DELETED + 1))
    else
      echo "FAIL   ${SUB_ID} ${ASSIGNMENT_ID##*/}"
      ERRORS=$((ERRORS + 1))
    fi
  done <<< "${ASSIGNMENTS}"
done

echo ""
echo "deleted=${DELETED} errors=${ERRORS}"
[[ ${ERRORS} -gt 0 ]] && exit 1
exit 0
