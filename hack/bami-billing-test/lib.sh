#!/bin/bash
# Shared helpers sourced by the other scripts. Not meant to be run directly.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BICEP_DIR="${REPO_ROOT}/demo/bicep"

# shellcheck source=./config.env
source "${SCRIPT_DIR}/config.env"

if [[ -z "${BAMI_SUBSCRIPTION_ID}" ]]; then
  echo "ERROR: BAMI_SUBSCRIPTION_ID is not set. Edit config.env or export it." >&2
  exit 1
fi

# Pin every az call to the BAMI subscription so we never touch another.
export SUBSCRIPTION="${BAMI_SUBSCRIPTION_ID}"

TENANT_ID="$(az account show --subscription "${SUBSCRIPTION}" --query tenantId -o tsv)"
export TENANT_ID

# Derived resource IDs, handy for `az resource show` and billing inspection.
export SUBSCRIPTION_RESOURCE_ID="/subscriptions/${SUBSCRIPTION}"
export RESOURCE_GROUP_RESOURCE_ID="${SUBSCRIPTION_RESOURCE_ID}/resourceGroups/${CUSTOMER_RG_NAME}"
export CLUSTER_RESOURCE_ID="${RESOURCE_GROUP_RESOURCE_ID}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/${CLUSTER_NAME}"

export CLUSTER_API_VERSION="2025-12-23-preview"

# Derive a valid node pool name from a VM SKU (Standard_D8s_v3 -> np-d8s-v3).
nodepool_name_for_sku() {
  echo "np-$(echo "${1#Standard_}" | tr '[:upper:]_' '[:lower:]-')"
}

# Verify the RP is registered and the chosen location supports HCP clusters.
verify_provider_and_location() {
  local provider_json
  provider_json="$(az provider show --namespace Microsoft.RedHatOpenShift --subscription "${SUBSCRIPTION}" -o json)"

  if [[ "Registered" != "$(echo "${provider_json}" | jq -r .registrationState)" ]]; then
    echo "ERROR: Microsoft.RedHatOpenShift is not registered on subscription ${SUBSCRIPTION}." >&2
    echo "       Register it with: az provider register --namespace Microsoft.RedHatOpenShift --subscription ${SUBSCRIPTION}" >&2
    exit 1
  fi

  local match
  match="$(echo "${provider_json}" | jq --arg location "${LOCATION}" -r \
    '.resourceTypes[] | select(.resourceType | ascii_downcase == "hcpopenshiftclusters") | .locations[] | select(. | ascii_downcase | gsub(" "; "") == $location)')"
  if [[ -z "${match}" ]]; then
    echo "ERROR: Location '${LOCATION}' is not supported for hcpOpenShiftClusters on subscription ${SUBSCRIPTION}." >&2
    exit 1
  fi
}

# Return 0 if every REQUIRED_AFEC_FLAGS flag is Registered, else 1 (no output).
afec_flags_all_registered() {
  local flag state ns="Microsoft.RedHatOpenShift"
  for flag in ${REQUIRED_AFEC_FLAGS}; do
    state="$(az feature show --namespace "${ns}" --name "${flag}" \
      --subscription "${SUBSCRIPTION}" --query 'properties.state' -o tsv 2>/dev/null || true)"
    [[ "${state}" == "Registered" ]] || return 1
  done
  return 0
}

# Verify the required AFEC flags are fully Registered. Approval is a manual
# Geneva Action, so fail fast with guidance instead of a later obscure error.
verify_afec_flags() {
  local flag state ns="Microsoft.RedHatOpenShift" bad=0
  for flag in ${REQUIRED_AFEC_FLAGS}; do
    state="$(az feature show --namespace "${ns}" --name "${flag}" \
      --subscription "${SUBSCRIPTION}" --query 'properties.state' -o tsv 2>/dev/null || true)"
    if [[ "${state}" != "Registered" ]]; then
      echo "ERROR: AFEC flag ${ns}/${flag} is '${state:-not found}', expected 'Registered'." >&2
      bad=1
    fi
  done
  if [[ "${bad}" -ne 0 ]]; then
    cat >&2 <<'EOF'
One or more AFEC flags are not Registered. Registration is a two-step process:
  1. ./prereqs.sh runs `az feature register` (puts flags in Registering/Pending).
  2. A Microsoft PlatformServiceAdministrator must APPROVE them via Geneva Action
     (ARM -> Feature Management -> Approve Feature Registration). This step is
     manual and cannot be scripted. See README.md "Reproducibility".
EOF
    exit 1
  fi
}

print_config() {
  cat <<EOF
BAMI billing test configuration
  Subscription : ${SUBSCRIPTION}
  Tenant       : ${TENANT_ID}
  Location     : ${LOCATION}
  Customer RG  : ${CUSTOMER_RG_NAME}
  Managed RG   : ${MANAGED_RESOURCE_GROUP}
  Cluster      : ${CLUSTER_NAME} (v${CLUSTER_VERSION})
  Node pools   : ${NODEPOOL_SPECS} (v${NODEPOOL_VERSION})
  Private KV   : ${PRIVATE_KEYVAULT}
  Cluster ID   : ${CLUSTER_RESOURCE_ID}
EOF
}
