#!/bin/bash
set -euo pipefail

# EventGrid MQTT clients pin `authenticationName` immutably. When a certificate
# SAN changes - for example when a regional DNS zone is renamed - ARM rejects the
# update with:
#
#   The authenticationName property for a client cannot be updated.
#
# and the deployment fails until the client is deleted by hand under JIT. This
# script reconciles that by deleting a client whose authenticationName no longer
# matches the desired SAN, so the ARM step that follows recreates it.
#
# It is a no-op when the client is absent or already correct.

API_VERSION="2023-12-15-preview"
MAX_RETRIES=30
RETRY_INTERVAL=5

: "${ClientName:?ClientName must be set}"
: "${AuthenticationName:?AuthenticationName must be set}"

# The namespace can be given either as a full ARM resource ID or as its parts.
if [[ -n "${EventGridNamespaceId:-}" ]]; then
  if [[ ! "${EventGridNamespaceId}" =~ ^/subscriptions/([^/]+)/resourceGroups/([^/]+)/providers/Microsoft\.EventGrid/namespaces/([^/]+)$ ]]; then
    echo "EventGridNamespaceId is not a well-formed EventGrid namespace resource ID: ${EventGridNamespaceId}" >&2
    exit 1
  fi
  EventGridSubscriptionId="${BASH_REMATCH[1]}"
  EventGridResourceGroup="${BASH_REMATCH[2]}"
  EventGridNamespaceName="${BASH_REMATCH[3]}"
else
  : "${EventGridSubscriptionId:?EventGridSubscriptionId or EventGridNamespaceId must be set}"
  : "${EventGridResourceGroup:?EventGridResourceGroup or EventGridNamespaceId must be set}"
  : "${EventGridNamespaceName:?EventGridNamespaceName or EventGridNamespaceId must be set}"
fi

CLIENT_URL="https://management.azure.com/subscriptions/${EventGridSubscriptionId}/resourceGroups/${EventGridResourceGroup}/providers/Microsoft.EventGrid/namespaces/${EventGridNamespaceName}/clients/${ClientName}?api-version=${API_VERSION}"

BODY_FILE="$(mktemp)"
STDERR_FILE="$(mktemp)"
trap 'rm -f "${BODY_FILE}" "${STDERR_FILE}"' EXIT

# Writes the client JSON to BODY_FILE and returns:
#   0 - client exists
#   1 - client does not exist
#   2 - the read itself failed
# Callers must treat 2 as fatal: a permissions failure must never be mistaken
# for an absent client, which would silently skip reconciliation. This cannot
# report the distinction by exiting, because callers invoke it from a subshell.
get_client() {
  local rc=0
  az rest --method GET --url "${CLIENT_URL}" >"${BODY_FILE}" 2>"${STDERR_FILE}" || rc=$?
  if [[ ${rc} -eq 0 ]]; then
    return 0
  fi
  if grep -qiE 'ResourceNotFound|was not found|\(404\)' "${STDERR_FILE}"; then
    return 1
  fi
  return 2
}

read_failed() {
  echo "failed to read EventGrid client ${ClientName}:" >&2
  cat "${STDERR_FILE}" >&2
  exit 1
}

echo "Reconciling EventGrid MQTT client ${ClientName} in namespace ${EventGridNamespaceName}"

rc=0
get_client || rc=$?
case ${rc} in
  0) ;;
  1)
    echo "Client ${ClientName} does not exist; the ARM deployment will create it."
    exit 0
    ;;
  *) read_failed ;;
esac

currentAuthName="$(jq -r '.properties.authenticationName // ""' <"${BODY_FILE}")"

if [[ "${currentAuthName}" == "${AuthenticationName}" ]]; then
  echo "Client ${ClientName} already authenticates as ${AuthenticationName}; nothing to reconcile."
  exit 0
fi

echo "authenticationName mismatch on ${ClientName}:"
echo "  current: ${currentAuthName}"
echo "  desired: ${AuthenticationName}"
echo "authenticationName is immutable; deleting the client so the ARM deployment recreates it."

az rest --method DELETE --url "${CLIENT_URL}"

# Wait for the delete to settle so the ARM step does not race a pending delete.
for i in $(seq 1 ${MAX_RETRIES}); do
  rc=0
  get_client || rc=$?
  case ${rc} in
    1)
      echo "Client ${ClientName} deleted."
      exit 0
      ;;
    2) read_failed ;;
  esac
  echo "Attempt ${i}/${MAX_RETRIES}: client still present, retrying in ${RETRY_INTERVAL}s..."
  sleep ${RETRY_INTERVAL}
done

echo "Client ${ClientName} still exists $((MAX_RETRIES * RETRY_INTERVAL))s after delete" >&2
exit 1
