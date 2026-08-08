#!/bin/bash
set -euo pipefail

# One-time pre-merge script: deletes the legacy role assignments created by
# grant-openshift-release-bot-dev.sh so that ci-bot-rbac-dev can recreate
# them with deterministic guid()-based names. Only assignments for the roles
# the declarative templates manage are removed; any other grant on the bot SP
# is left untouched.
#
# Requires Owner or User Access Administrator on each subscription.
# Dry-run by default; set APPLY=1 to actually delete.

if [ "${APPLY:-}" = "1" ]; then
  DRY_RUN_MODE=false
else
  echo "DRY_RUN mode (default) - will only show what would be deleted. Set APPLY=1 to delete."
  DRY_RUN_MODE=true
fi

az account show -o none 2>/dev/null || { echo "ERROR: not logged in to Azure"; exit 1; }

if ! BOT_SP_IDS=$(az ad sp list --display-name "OpenShift Release Bot" --query '[].id' -o tsv); then
  echo "ERROR: failed to look up the 'OpenShift Release Bot' service principal (see az output above)"
  exit 1
fi
SP_COUNT=$(grep -c . <<<"${BOT_SP_IDS}" || true)
if [[ "${SP_COUNT}" -ne 1 ]]; then
  echo "ERROR: expected exactly 1 'OpenShift Release Bot' SP, found ${SP_COUNT}"
  exit 1
fi
BOT_SP_ID="${BOT_SP_IDS}"
echo "Bot SP objectId: ${BOT_SP_ID}"

SUBSCRIPTIONS=(
  "1d3378d3-5a3f-4712-85a1-2485495dfc4b"
  "974ebd46-8ad3-41e3-afef-7ef25fd5c371"
  "e8c5a115-842d-4d7e-98ad-cfb2c50b209e"
  "0ef1ad54-9296-44cd-9600-5dc8e9a74034"
  "e627aa70-36a3-40b0-8e68-975269e39d7b"
  "6ed122d1-7e03-4a01-baae-9020abf350d4"
)

# Role definition GUIDs the declarative pipeline (ci-bot-rbac-subscription.bicep)
# recreates on EVERY managed subscription. The cleanup only deletes assignments
# for roles the pipeline will recreate, so any unrelated grant on the bot SP is
# preserved.
BASE_ROLE_IDS=(
  "b24988ac-6180-42a0-ab88-20f7382dd24c"  # Contributor
  "f58310d9-a9f6-439a-9e8d-f62e7b41a168"  # Role Based Access Control Administrator
  "b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b"  # Azure Kubernetes Service RBAC Cluster Admin
)
KEY_VAULT_ADMIN_ROLE="00482a5a-887f-4fb3-b363-3b7fe8e74483"  # Key Vault Administrator
GRAFANA_ADMIN_ROLE="22926164-76b3-42b3-bc55-97df8dab3e41"    # Grafana Admin

# Roles the pipeline grants only to specific subscriptions (mirrors
# config-dev-ci.yaml at migration time): Key Vault Admin to the infra subs with
# grantKeyVaultAdmin=true, and Grafana Admin additionally to the global sub.
# Deleting these roles from any other subscription would strip an assignment the
# pipeline never recreates there.
declare -A EXTRA_ROLE_IDS=(
  ["1d3378d3-5a3f-4712-85a1-2485495dfc4b"]="${KEY_VAULT_ADMIN_ROLE} ${GRAFANA_ADMIN_ROLE}"
  ["0ef1ad54-9296-44cd-9600-5dc8e9a74034"]="${KEY_VAULT_ADMIN_ROLE}"
)

DELETED=0
KEPT=0
ERRORS=0
for SUB_ID in "${SUBSCRIPTIONS[@]}"; do
  # Roles the pipeline recreates in THIS subscription = base roles + any extras.
  MANAGED_ROLES="${BASE_ROLE_IDS[*]} ${EXTRA_ROLE_IDS[${SUB_ID}]:-}"

  # assignment id, role definition id, friendly role name, and scope per bot grant
  if ! ASSIGNMENTS=$(az role assignment list \
    --assignee "${BOT_SP_ID}" \
    --subscription "${SUB_ID}" \
    --query '[].[id, roleDefinitionId, roleDefinitionName, scope]' -o tsv); then
    echo "ERROR  ${SUB_ID} (failed to list role assignments — legacy grants may remain)"
    ERRORS=$((ERRORS + 1))
    continue
  fi

  if [[ -z "${ASSIGNMENTS}" ]]; then
    echo "NONE   ${SUB_ID}"
    continue
  fi

  while IFS=$'\t' read -r ASSIGNMENT_ID ROLE_DEF_ID ROLE_NAME SCOPE; do
    [[ -z "${ASSIGNMENT_ID}" ]] && continue
    # The legacy script granted these at subscription scope; leave any
    # narrower-scope (resource-group/resource) assignment untouched.
    if [[ "${SCOPE}" != "/subscriptions/${SUB_ID}" ]]; then
      echo "KEEP   ${SUB_ID} ${ROLE_NAME} (scope ${SCOPE}, not subscription-scope)"
      KEPT=$((KEPT + 1))
      continue
    fi
    ROLE_GUID="${ROLE_DEF_ID##*/}"
    if [[ " ${MANAGED_ROLES} " != *" ${ROLE_GUID} "* ]]; then
      echo "KEEP   ${SUB_ID} ${ROLE_NAME} (not recreated in this subscription)"
      KEPT=$((KEPT + 1))
      continue
    fi
    if [ "$DRY_RUN_MODE" = true ]; then
      echo "WOULD  ${SUB_ID} ${ROLE_NAME}"
    elif az role assignment delete --ids "${ASSIGNMENT_ID}"; then
      echo "DONE   ${SUB_ID} ${ROLE_NAME}"
      DELETED=$((DELETED + 1))
    else
      echo "FAIL   ${SUB_ID} ${ROLE_NAME}"
      ERRORS=$((ERRORS + 1))
    fi
  done <<< "${ASSIGNMENTS}"
done

echo ""
echo "deleted=${DELETED} kept=${KEPT} errors=${ERRORS}"
[[ ${ERRORS} -gt 0 ]] && exit 1
exit 0
