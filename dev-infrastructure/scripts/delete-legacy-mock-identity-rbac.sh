#!/bin/bash
set -euo pipefail

# One-time pre-merge migration script: deletes the legacy role assignments that
# the removed `create-mock-identities` / `create-int-mock-identities` Makefile
# targets (via create-sp-for-rbac.sh) created for the mock identity service
# principals under random (non-deterministic) names.
#
# mock-identity-rbac.bicep recreates these assignments with deterministic
# guid()-based names. ARM rejects a new role assignment for an already-assigned
# (principal, role, scope) triple regardless of name (RoleAssignmentExists / 409),
# so the first privileged rollout of the `mock-identities` group would fail until
# the legacy assignments are removed. Run this ONCE, before that first
# `make dev-ci-privileged-local-run`.
#
# Only the DEV home subscription and the INT home subscription are affected: the
# DEV E2E customer subscriptions were already pipeline-managed with deterministic
# names, so they do not collide.
#
# Requires Owner or User Access Administrator on each target subscription.
# Defaults to a dry run; set APPLY=1 to actually delete.

if [ "${APPLY:-}" = "1" ] || [ "${APPLY:-}" = "true" ]; then
  DRY_RUN_MODE=false
else
  echo "DRY_RUN mode (default) - will only show what would be deleted. Set APPLY=1 to delete."
  DRY_RUN_MODE=true
fi

az account show -o none 2>/dev/null || { echo "ERROR: not logged in to Azure"; exit 1; }

# DEV home (global) subscription and INT home subscription. Keep in sync with
# config/config-dev-ci.yaml (.ci.dev.infrastructureSubscriptions isGlobalSubscription
# and .ci.int.e2eSubscriptions).
DEV_SUB="1d3378d3-5a3f-4712-85a1-2485495dfc4b"
INT_SUB="64f0619f-ebc2-4156-9d91-c4c781de7e54"

DEV_POOL_SIZE="${DEV_POOL_SIZE:-20}"
DEV_POOL_BASE="aro-dev-msi-mock-pool"

DEV_APPS=(
  "aro-dev-first-party2"
  "aro-dev-arm-helper2"
  "aro-dev-msi-mock2"
)
for i in $(seq 0 $((DEV_POOL_SIZE - 1))); do
  DEV_APPS+=("${DEV_POOL_BASE}-${i}")
done

INT_APPS=(
  "aro-hcp-int-fp"
  "aro-hcp-int-arm-helper"
  "aro-hcp-int-msi-mock"
)

DELETED=0
ERRORS=0

# cleanup_principal <subscription-id> <app-display-name>
# Deletes every role assignment held by the app's service principal on the given
# subscription. The privileged rollout recreates the intended ones deterministically.
cleanup_principal() {
  local SUB_ID="$1"
  local APP_NAME="$2"

  local SP_COUNT
  SP_COUNT=$(az ad sp list --display-name "${APP_NAME}" --query 'length(@)' -o tsv 2>/dev/null || echo 0)
  if [[ "${SP_COUNT}" -eq 0 ]]; then
    echo "SKIP   ${SUB_ID} ${APP_NAME} (SP not found)"
    return
  fi
  if [[ "${SP_COUNT}" -ne 1 ]]; then
    echo "ERROR  ${SUB_ID} ${APP_NAME} (expected 1 SP, found ${SP_COUNT})"
    ERRORS=$((ERRORS + 1))
    return
  fi

  local SP_ID
  SP_ID=$(az ad sp list --display-name "${APP_NAME}" --query '[0].id' -o tsv 2>/dev/null)

  local ASSIGNMENTS
  ASSIGNMENTS=$(az role assignment list \
    --assignee "${SP_ID}" \
    --subscription "${SUB_ID}" \
    --query '[].id' -o tsv 2>/dev/null) || {
    echo "SKIP   ${SUB_ID} ${APP_NAME} (no access)"
    return
  }

  if [[ -z "${ASSIGNMENTS}" ]]; then
    echo "NONE   ${SUB_ID} ${APP_NAME}"
    return
  fi

  while IFS= read -r ASSIGNMENT_ID; do
    [[ -z "${ASSIGNMENT_ID}" ]] && continue
    if [ "$DRY_RUN_MODE" = true ]; then
      local ROLE
      ROLE=$(az role assignment list --subscription "${SUB_ID}" \
        --query "[?id=='${ASSIGNMENT_ID}'].roleDefinitionName | [0]" -o tsv 2>/dev/null)
      echo "WOULD  ${SUB_ID} ${APP_NAME} ${ROLE}"
    elif az role assignment delete --ids "${ASSIGNMENT_ID}" 2>/dev/null; then
      echo "DONE   ${SUB_ID} ${APP_NAME} ${ASSIGNMENT_ID##*/}"
      DELETED=$((DELETED + 1))
    else
      echo "FAIL   ${SUB_ID} ${APP_NAME} ${ASSIGNMENT_ID##*/}"
      ERRORS=$((ERRORS + 1))
    fi
  done <<< "${ASSIGNMENTS}"
}

for APP_NAME in "${DEV_APPS[@]}"; do
  cleanup_principal "${DEV_SUB}" "${APP_NAME}"
done

for APP_NAME in "${INT_APPS[@]}"; do
  cleanup_principal "${INT_SUB}" "${APP_NAME}"
done

echo ""
echo "deleted=${DELETED} errors=${ERRORS}"
[[ ${ERRORS} -gt 0 ]] && exit 1
exit 0
