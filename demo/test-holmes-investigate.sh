#!/bin/bash
# test-holmes-investigate.sh - Test Holmes investigation against an HCP cluster
#
# Runs an investigation via the admin API's /investigate endpoint against
# a personal dev RP frontend (port-forwarded).
#
# Prerequisites:
#   - Personal dev environment deployed with Holmes changes
#   - An HCP cluster created (via deploy-hcp-local.sh or similar)
#   - Port-forward to admin API OR this script sets one up automatically
#
# Usage:
#   # With auto port-forward (requires hcpctl):
#   ./demo/test-holmes-investigate.sh "why are pods crashlooping?"
#
#   # With manual port-forward already running:
#   ADMIN_API_URL=http://localhost:9443 ./demo/test-holmes-investigate.sh "what is the cluster health?"
#
#   # Override cluster resource ID:
#   CLUSTER_RESOURCE_ID=/subscriptions/.../hcpOpenShiftClusters/mycluster \
#     ./demo/test-holmes-investigate.sh "check node status"

set -o errexit
set -o nounset
set -o pipefail

QUESTION="${1:-what is the cluster health status?}"
SCOPE="${SCOPE:-dataplane}"
ADMIN_API_URL="${ADMIN_API_URL:-}"
ADMIN_API_PORT="${ADMIN_API_PORT:-9443}"
TIMEOUT="${TIMEOUT:-600}"

# Derive cluster resource ID from env_vars if not set
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [[ -z "${CLUSTER_RESOURCE_ID:-}" ]]; then
    source "${SCRIPT_DIR}/env_vars" 2>/dev/null || true
    SUBSCRIPTION_ID="${SUBSCRIPTION_ID:-$(az account show --query id -o tsv)}"
    CUSTOMER_RG_NAME="${CUSTOMER_RG_NAME:-${USER}-holmes-test2}"
    CLUSTER_NAME="${CLUSTER_NAME:-${USER}}"
    CLUSTER_RESOURCE_ID="/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/${CLUSTER_NAME}"
fi

echo "Holmes Investigation Test"
echo "  Cluster:  ${CLUSTER_RESOURCE_ID}"
echo "  Question: ${QUESTION}"
echo "  Scope:    ${SCOPE}"
echo ""

# Set up port-forward if no URL provided
PF_PID=""
cleanup() {
    if [[ -n "${PF_PID}" ]]; then
        kill "${PF_PID}" 2>/dev/null || true
    fi
}
trap cleanup EXIT

if [[ -z "${ADMIN_API_URL}" ]]; then
    ADMIN_API_URL="http://localhost:${ADMIN_API_PORT}"

    # Check if port is already in use
    if lsof -i ":${ADMIN_API_PORT}" &>/dev/null; then
        echo "Port ${ADMIN_API_PORT} already in use, assuming admin API port-forward is running"
    else
        echo "Setting up port-forward to admin API..."
        REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
        SVC_KUBECONFIG="${SVC_KUBECONFIG:-$(make -C "${REPO_ROOT}" -s infra.svc.aks.kubeconfigfile 2>/dev/null || echo "")}"
        if [[ -z "${SVC_KUBECONFIG}" ]]; then
            echo "ERROR: Cannot determine service cluster kubeconfig."
            echo "Set SVC_KUBECONFIG env var to the service cluster kubeconfig path."
            exit 1
        fi
        kubectl --kubeconfig="${SVC_KUBECONFIG}" port-forward svc/admin-api "${ADMIN_API_PORT}:8443" -n aro-hcp-admin-api &
        PF_PID=$!
        sleep 5
    fi
fi

echo "==> Sending investigation request to ${ADMIN_API_URL}..."
echo ""

HTTP_CODE=$(curl --silent --show-error \
    --output /tmp/holmes-investigate-output.txt \
    --write-out "%{http_code}" \
    --request POST \
    --header "Content-Type: application/json" \
    --header "X-Ms-Client-Principal-Name: ${USER}@redhat.com" \
    --header "X-Ms-Client-Principal-Type: User" \
    --max-time "${TIMEOUT}" \
    --data "{\"question\": \"${QUESTION}\", \"scope\": \"${SCOPE}\"}" \
    "${ADMIN_API_URL}/admin/v1/hcp${CLUSTER_RESOURCE_ID}/investigate")

echo ""
if [[ "${HTTP_CODE}" -ge 200 ]] && [[ "${HTTP_CODE}" -lt 300 ]]; then
    echo "==> Investigation complete (HTTP ${HTTP_CODE})"
    echo ""
    cat /tmp/holmes-investigate-output.txt
else
    echo "==> Investigation failed (HTTP ${HTTP_CODE})"
    echo ""
    cat /tmp/holmes-investigate-output.txt
    exit 1
fi
