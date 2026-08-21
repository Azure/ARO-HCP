#!/bin/bash
# Run istio upgrade against pers-dev cluster using ARO-Tools binary.
#
# Usage:
#   ./hack/istio-upgrade-pers.sh                  # dry-run with asm-1-29
#   ./hack/istio-upgrade-pers.sh asm-1-30         # dry-run with asm-1-30
#   DRY_RUN=false ./hack/istio-upgrade-pers.sh    # live upgrade
#   STOP_AFTER=canary-start ./hack/istio-upgrade-pers.sh  # halt for staged rollout
#
# Prerequisites:
#   - az login (active session)
#   - kubectl context set to pers-dev svc cluster
#   - ARO-Tools repo cloned at ~/Code/ARO-Tools (or set ARO_TOOLS_DIR)

set -euo pipefail

ARO_TOOLS_DIR="${ARO_TOOLS_DIR:-$HOME/Code/ARO-Tools}"
BINARY="/tmp/istio-upgrade"

export AZURE_TOKEN_CREDENTIALS="${AZURE_TOKEN_CREDENTIALS:-AzureCLICredential}"

echo "Building istio-upgrade from ${ARO_TOOLS_DIR}/tools/istio-upgrade ..."
CGO_ENABLED=0 go build -C "${ARO_TOOLS_DIR}/tools/istio-upgrade" -o "${BINARY}" main.go

STOP_AFTER_FLAG=""
if [[ -n "${STOP_AFTER:-}" ]]; then
  STOP_AFTER_FLAG="--stop-after ${STOP_AFTER}"
fi

"${BINARY}" run \
  --subscription-id "$(az account show --query id -o tsv)" \
  --resource-group hcp-underlay-pers-usw3trwi-svc \
  --cluster-name pers-usw3trwi-svc \
  --kubeconfig ~/.kube/config \
  --versions "${1:-asm-1-29}" \
  --tag "prod-stable" \
  --ingress-ip-name "aro-hcp-istio-ingress" \
  --region-rg hcp-underlay-pers-usw3trwi \
  --dry-run="${DRY_RUN:-true}" \
  ${STOP_AFTER_FLAG}
