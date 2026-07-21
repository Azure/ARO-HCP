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

# Build the subjectAlternativeNames.dnsNames JSON array from the comma-separated
# list, trimming surrounding whitespace and dropping empty entries.
dns_json="$(printf '%s' "${DNS_NAMES}" | tr ',' '\n' | \
  awk '{ gsub(/^[[:space:]]+|[[:space:]]+$/, ""); if ($0 != "") a[++n] = $0 }
       END { for (i = 1; i <= n; i++) printf "%s\"%s\"", (i > 1 ? "," : ""), a[i] }')"
if [[ -z "${dns_json}" ]]; then
  echo "ERROR: DNS_NAMES contained no valid entries after parsing: '${DNS_NAMES}'" >&2
  exit 1
fi

policy="$(cat <<JSON
{
  "issuerParameters": { "name": "Self" },
  "keyProperties": { "exportable": true, "keyType": "RSA", "keySize": 2048, "reuseKey": false },
  "secretProperties": { "contentType": "application/x-pkcs12" },
  "x509CertificateProperties": {
    "subject": "${SUBJECT}",
    "subjectAlternativeNames": { "dnsNames": [${dns_json}] },
    "keyUsage": ["digitalSignature", "keyEncipherment"],
    "validityInMonths": ${VALIDITY_IN_MONTHS}
  },
  "lifetimeActions": [
    { "trigger": { "lifetimePercentage": ${RENEW_PERCENTAGE_LIFETIME} }, "action": { "actionType": "AutoRenew" } }
  ]
}
JSON
)"

echo "Creating self-signed certificate '${CERT_NAME}' in '${VAULT_NAME}' (subject ${SUBJECT})..."
az keyvault certificate create \
  --vault-name "${VAULT_NAME}" \
  --name "${CERT_NAME}" \
  --policy "${policy}"
