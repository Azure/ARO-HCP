#!/bin/bash
set -euo pipefail

# Creates a self-signed certificate in Azure Key Vault.
#
# Replaces the former key-vault-cert.bicep deploymentScript (ARO-28515): Bicep
# cannot create Key Vault certificates, which previously forced a
# Microsoft.Resources/deploymentScripts resource. All of that module's callers
# used the "Self" issuer, so the equivalent is a plain
# `az keyvault certificate create` with a self-signed policy.
#
# Idempotent: if the certificate already exists it is left untouched unless
# FORCE=true is set, so re-running the dev bootstrap does not rotate certs out
# from under the service principals that were created from them.
#
# Required env/args:
#   VAULT_NAME   Key Vault name
#   CERT_NAME    certificate name
#   SUBJECT      certificate subject, e.g. CN=firstparty.hcp.osadev.cloud
#   DNS_NAMES    comma-separated DNS SANs
# Optional:
#   VALIDITY_IN_MONTHS        default 120
#   RENEW_PERCENTAGE_LIFETIME default 24
#   FORCE                     "true" to recreate an existing certificate

VAULT_NAME="${VAULT_NAME:?VAULT_NAME is required}"
CERT_NAME="${CERT_NAME:?CERT_NAME is required}"
SUBJECT="${SUBJECT:?SUBJECT is required}"
DNS_NAMES="${DNS_NAMES:?DNS_NAMES is required}"
VALIDITY_IN_MONTHS="${VALIDITY_IN_MONTHS:-120}"
RENEW_PERCENTAGE_LIFETIME="${RENEW_PERCENTAGE_LIFETIME:-24}"
FORCE="${FORCE:-false}"

# Validate the numeric policy inputs up front so a bad value fails clearly here
# instead of producing invalid policy JSON that az rejects with an opaque error.
if [[ ! "${VALIDITY_IN_MONTHS}" =~ ^[1-9][0-9]*$ ]]; then
  echo "ERROR: VALIDITY_IN_MONTHS must be a positive integer, got '${VALIDITY_IN_MONTHS}'." >&2
  exit 1
fi
if [[ ! "${RENEW_PERCENTAGE_LIFETIME}" =~ ^[1-9][0-9]*$ ]] || (( RENEW_PERCENTAGE_LIFETIME > 99 )); then
  echo "ERROR: RENEW_PERCENTAGE_LIFETIME must be an integer between 1 and 99, got '${RENEW_PERCENTAGE_LIFETIME}'." >&2
  exit 1
fi

# RFC 5280 requires the common name be <= 64 characters.
cn="${SUBJECT#CN=}"
if [[ "${SUBJECT}" == CN=* && ${#cn} -gt 64 ]]; then
  echo "ERROR: CN '${cn}' exceeds 64 characters (RFC 5280)." >&2
  exit 1
fi

if [[ "${FORCE}" != "true" ]] && \
   az keyvault certificate show --vault-name "${VAULT_NAME}" --name "${CERT_NAME}" >/dev/null 2>&1; then
  echo "Certificate '${CERT_NAME}' already exists in '${VAULT_NAME}', skipping (set FORCE=true to recreate)."
  exit 0
fi

# Build the subjectAlternativeNames.dnsNames list from the comma-separated
# input, trimming surrounding whitespace and dropping empty entries.
dns_names=()
while IFS= read -r entry || [[ -n "${entry}" ]]; do
  entry="${entry#"${entry%%[![:space:]]*}"}"
  entry="${entry%"${entry##*[![:space:]]}"}"
  [[ -n "${entry}" ]] && dns_names+=("${entry}")
done < <(printf '%s' "${DNS_NAMES}" | tr ',' '\n')
if [[ ${#dns_names[@]} -eq 0 ]]; then
  echo "ERROR: DNS_NAMES contained no valid entries after parsing: '${DNS_NAMES}'" >&2
  exit 1
fi

# Construct the policy with jq so SUBJECT and the DNS names are JSON-escaped,
# avoiding invalid JSON (or injection) when they contain special characters.
policy="$(jq -n \
  --arg subject "${SUBJECT}" \
  --argjson validityInMonths "${VALIDITY_IN_MONTHS}" \
  --argjson renewPercentage "${RENEW_PERCENTAGE_LIFETIME}" \
  --args '
  {
    issuerParameters: { name: "Self" },
    keyProperties: { exportable: true, keyType: "RSA", keySize: 2048, reuseKey: false },
    secretProperties: { contentType: "application/x-pkcs12" },
    x509CertificateProperties: {
      subject: $subject,
      subjectAlternativeNames: { dnsNames: $ARGS.positional },
      keyUsage: ["digitalSignature", "keyEncipherment"],
      validityInMonths: $validityInMonths
    },
    lifetimeActions: [
      { trigger: { lifetimePercentage: $renewPercentage }, action: { actionType: "AutoRenew" } }
    ]
  }' "${dns_names[@]}")"

echo "Creating self-signed certificate '${CERT_NAME}' in '${VAULT_NAME}' (subject ${SUBJECT})..."
az keyvault certificate create \
  --vault-name "${VAULT_NAME}" \
  --name "${CERT_NAME}" \
  --policy "${policy}"
