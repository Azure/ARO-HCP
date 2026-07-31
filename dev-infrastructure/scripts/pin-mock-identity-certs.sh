#!/bin/bash
set -euo pipefail

# Pin each mock identity's Key Vault certificate onto its Entra application as a
# keyCredential (AsymmetricX509Cert / Verify).
#
# WHY THIS EXISTS
#   templates/mock-identity-apps.bicep creates the mock Entra apps and their
#   service principals but does NOT configure authentication on them. The mock
#   certificates are self-signed and Clusters Service authenticates in
#   send-certificate-chain mode, so SNI trust (trustedSubjectNameAndIssuers) does
#   not validate (Entra rejects the self-signed chain with AADSTS7000213). The
#   proven mechanism is to pin the certificate's public key on the app and let
#   Entra match the presented leaf's thumbprint against it. Bicep cannot read Key
#   Vault certificate material, so the pinning is done here (see the header of
#   templates/mock-identity-apps.bicep for the full rationale).
#
#   This mirrors the pre-migration `az ad app credential reset --keyvault --cert`
#   behaviour but is idempotent: it PATCHes keyCredentials to contain exactly the
#   current Key Vault certificate, so re-running (e.g. after a certificate
#   rotation) simply re-pins the current cert without accumulating duplicates.
#
# REQUIREMENTS
#   The caller must be able to (a) read certificates from ${VAULT_NAME} (Key Vault
#   Certificate/Secret User or Administrator on that vault) and (b) write
#   keyCredentials on the mock apps (owner of the apps, or Application.ReadWrite).
#   Run it right after deploying templates/mock-identity-apps.bicep and after the
#   certificates exist (`make create-mock-identity-certs` /
#   `make create-int-mock-identity-certs`).
#
# ENVIRONMENT VARIABLES
#   VAULT_NAME     (required) Key Vault holding the certificates.
#   VAULT_SUBSCRIPTION (optional) Subscription containing the Key Vault. Set
#                  this when it differs from the pipeline step's subscription.
#   IDENTITIES     (required) Newline-separated "appDisplayName=certName" pairs
#                  for the named identities.
#   POOL_APP_BASE  (optional) Base application name for pooled identities.
#   POOL_CERT_BASE (optional) Base certificate name for pooled identities
#                  (member cert name is "${POOL_CERT_BASE}-${i}").
#   POOL_SIZE      (optional, default 0) Number of pool members (0..N-1).

: "${VAULT_NAME:?VAULT_NAME is required}"
: "${IDENTITIES:?IDENTITIES is required (newline-separated appDisplayName=certName pairs)}"
POOL_APP_BASE="${POOL_APP_BASE:-}"
POOL_CERT_BASE="${POOL_CERT_BASE:-}"
POOL_SIZE="${POOL_SIZE:-0}"
VAULT_SUBSCRIPTION="${VAULT_SUBSCRIPTION:-}"

GRAPH="https://graph.microsoft.com/v1.0"

resolve_app_id() {
  az ad app list --filter "displayName eq '$1'" --query "[0].appId" -o tsv 2>/dev/null
}

# pin <appDisplayName> <certName>
pin() {
  local display="$1" cert="$2" app_id cer body_file
  local -a vault_args=()

  if [[ -n "${VAULT_SUBSCRIPTION}" ]]; then
    vault_args+=(--subscription "${VAULT_SUBSCRIPTION}")
  fi

  app_id="$(resolve_app_id "${display}")"
  if [[ -z "${app_id}" || "${app_id}" == "None" ]]; then
    echo "ERROR: could not resolve appId for application '${display}'" >&2
    return 1
  fi

  cer="$(az keyvault certificate show "${vault_args[@]}" --vault-name "${VAULT_NAME}" --name "${cert}" --query cer -o tsv 2>/dev/null || true)"
  if [[ -z "${cer}" ]]; then
    echo "ERROR: could not read certificate '${cert}' from vault '${VAULT_NAME}'" >&2
    echo "       (needs a Key Vault data-plane role and network access to the vault)" >&2
    return 1
  fi

  echo "  ${display}  appId=${app_id}  cert=${cert}  -> pinning keyCredential"
  body_file="$(mktemp)"
  cat >"${body_file}" <<JSON
{"keyCredentials":[{"type":"AsymmetricX509Cert","usage":"Verify","key":"${cer}"}]}
JSON
  az rest --method PATCH \
    --url "${GRAPH}/applications(appId='${app_id}')" \
    --headers "Content-Type=application/json" \
    --body "@${body_file}" --output none
  rm -f "${body_file}"
}

echo "== Pin mock-identity certificates (VAULT=${VAULT_NAME}, POOL_SIZE=${POOL_SIZE}) =="

failures=0

echo "-- named identities --"
while IFS= read -r row; do
  row="$(echo "${row}" | xargs)"          # trim surrounding whitespace
  [[ -z "${row}" ]] && continue
  display="${row%%=*}"
  cert="${row#*=}"
  if [[ -z "${display}" || -z "${cert}" || "${display}" == "${row}" ]]; then
    echo "ERROR: malformed IDENTITIES entry '${row}' (expected appDisplayName=certName)" >&2
    failures=$((failures + 1))
    continue
  fi
  pin "${display}" "${cert}" || failures=$((failures + 1))
done <<< "${IDENTITIES}"

if [[ "${POOL_SIZE}" -gt 0 ]]; then
  : "${POOL_APP_BASE:?POOL_APP_BASE is required when POOL_SIZE > 0}"
  : "${POOL_CERT_BASE:?POOL_CERT_BASE is required when POOL_SIZE > 0}"
  echo "-- pooled identities (0..$((POOL_SIZE - 1))) --"
  for i in $(seq 0 $((POOL_SIZE - 1))); do
    pin "${POOL_APP_BASE}-${i}" "${POOL_CERT_BASE}-${i}" || failures=$((failures + 1))
  done
fi

echo
echo "== Verification (keyCredentials count per app) =="
verify() {
  local display="$1" app_id n
  app_id="$(resolve_app_id "${display}")"
  [[ -z "${app_id}" || "${app_id}" == "None" ]] && { echo "  ${display}: <no appId>"; return; }
  n="$(az rest --method GET \
        --url "${GRAPH}/applications(appId='${app_id}')?\$select=keyCredentials" \
        --query "length(keyCredentials)" -o tsv 2>/dev/null || echo '?')"
  echo "  ${display}: keyCredentials=${n}"
}
while IFS= read -r row; do
  row="$(echo "${row}" | xargs)"; [[ -z "${row}" ]] && continue
  verify "${row%%=*}"
done <<< "${IDENTITIES}"
if [[ "${POOL_SIZE}" -gt 0 ]]; then
  for i in $(seq 0 $((POOL_SIZE - 1))); do verify "${POOL_APP_BASE}-${i}"; done
fi

if [[ "${failures}" -ne 0 ]]; then
  echo "ERROR: ${failures} identity/identities failed to pin" >&2
  exit 1
fi
echo "Done. All mock identity certificates pinned in ${VAULT_NAME}."
