#!/bin/bash
# One-time per subscription: register providers + initiate AFEC flags.
# `az feature register` only initiates; final approval is a manual Geneva
# Action (see README.md). Idempotent -- safe to rerun as a status check.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
source "${SCRIPT_DIR}/lib.sh"

print_config
echo

echo "==> Registering resource providers"
for ns in ${REQUIRED_PROVIDERS}; do
  echo "    az provider register --namespace ${ns}"
  az provider register --namespace "${ns}" --subscription "${SUBSCRIPTION}" >/dev/null
done

echo "==> Initiating AFEC feature registration"
for flag in ${REQUIRED_AFEC_FLAGS}; do
  echo "    az feature register --name ${flag}"
  az feature register --namespace Microsoft.RedHatOpenShift --name "${flag}" \
    --subscription "${SUBSCRIPTION}" >/dev/null
done

echo
echo "==> Current AFEC flag states:"
az feature list --namespace Microsoft.RedHatOpenShift \
  --subscription "${SUBSCRIPTION}" -o table \
  | grep -E "$(echo "${REQUIRED_AFEC_FLAGS}" | tr ' ' '|')" || true

if afec_flags_all_registered; then
  echo
  echo "==> All AFEC flags are already Registered. No manual approval needed -- run ./setup.sh."
  exit 0
fi

cat <<EOF

==> Next (MANUAL) step -- cannot be scripted:
    A Microsoft PlatformServiceAdministrator must approve the flags via Geneva Actions:
      Azure Resource Manager -> Feature Management -> Approve Feature Registration
        Namespace:     Microsoft.RedHatOpenShift
        Feature Names: ${REQUIRED_AFEC_FLAGS}
        Subscription:  ${SUBSCRIPTION}

    After approval, re-register the provider so the flags take effect:
      az provider register --namespace Microsoft.RedHatOpenShift --subscription ${SUBSCRIPTION}

    Then verify all flags show 'Registered':
      az feature list --namespace Microsoft.RedHatOpenShift --subscription ${SUBSCRIPTION} -o table

    Once Registered, run ./setup.sh (it re-verifies the flags before deploying).
EOF
