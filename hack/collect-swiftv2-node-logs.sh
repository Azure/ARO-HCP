#!/usr/bin/env bash
# ICM 832382845 - SWIFT v2 delegated-NIC (VF) churn log collection.
# Read-only: creates its own debug pods, pulls
# /host/var/log/azure-vnet* + /host/var/run/azure-cns state,
# tars them, and deletes ONLY the debug pods it created.
#
# Run under JIT against the affected mgmt AKS (kubeconfig already pointed at it).
# Optional: set NODES_FILTER to a grep pattern to target specific node(s), e.g.
#   NODES_FILTER='vmss000004' ./collect-swiftv2-node-logs.sh
# Default (unset) collects from ALL nodes.

set -uo pipefail
set -x

OUTPUT_DIR="$(mktemp -d)"
TARBALL="node-logs-$(date +%Y%m%dT%H%M%S).tar.gz"
IMAGE="mcr.microsoft.com/cbl-mariner/base/core:2.0"
PODS_TO_DELETE=()

cleanup() {
  echo "Cleaning up debug pods..."
  for pod in "${PODS_TO_DELETE[@]+"${PODS_TO_DELETE[@]}"}"; do
    kubectl delete pod "${pod}" --ignore-not-found 2>/dev/null &
  done
  wait
}
trap cleanup EXIT

echo "Collecting logs into ${OUTPUT_DIR} ..."

mapfile -t nodes < <(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' \
  | { if [[ -n "${NODES_FILTER:-}" ]]; then grep -E "${NODES_FILTER}"; else cat; fi; })

# Create debug pods sequentially (wait/collection parallelised below)
declare -A node_pods
for node in "${nodes[@]}"; do
  create_output=$(kubectl debug "node/${node}" --image "${IMAGE}" -- sleep 3600 2>&1)
  echo "${create_output}"
  pod=$(echo "${create_output}" | grep -oP 'pod[/ ]\K[a-z0-9][-a-z0-9]*' | head -1)
  if [[ -n "${pod}" ]]; then
    node_pods["${node}"]="${pod}"
    PODS_TO_DELETE+=("${pod}")
  else
    echo "WARNING: could not determine debug pod name for ${node}"
  fi
done

# Wait for all pods to be ready in parallel
wait_pids=()
wait_failed=0
for node in "${!node_pods[@]}"; do
  kubectl wait --for=condition=Ready "pod/${node_pods[${node}]}" --timeout=120s &
  wait_pids+=("$!")
done
for pid in "${wait_pids[@]}"; do
  if ! wait "${pid}"; then
    wait_failed=1
  fi
done
if [[ "${wait_failed}" -ne 0 ]]; then
  echo "ERROR: one or more debug pods did not become Ready; aborting log collection." >&2
  exit 1
fi

# Collect logs from all nodes in parallel
collect_node() {
  local node="$1"
  local pod="$2"
  local node_dir="${OUTPUT_DIR}/${node}"
  mkdir -p "${node_dir}/var-log" "${node_dir}/var-run-azure-cns"

  echo "  [${node}] Collecting azure-vnet logs..."
  if ! kubectl exec "${pod}" -- sh -c 'cd /host/var/log && tar cf - azure-vnet* 2>/dev/null' \
    | tar xf - -C "${node_dir}/var-log" 2>/dev/null; then
    echo "  [${node}] (no azure-vnet logs found)"
  fi

  echo "  [${node}] Collecting azure-cns state..."
  if ! kubectl exec "${pod}" -- sh -c 'cd /host/var/run/azure-cns && tar cf - . 2>/dev/null' \
    | tar xf - -C "${node_dir}/var-run-azure-cns" 2>/dev/null; then
    echo "  [${node}] (no azure-cns state found)"
  fi

  echo "  [${node}] Done."
}

for node in "${!node_pods[@]}"; do
  collect_node "${node}" "${node_pods[${node}]}" &
done
wait

echo "Creating tarball ${TARBALL} ..."
tar czf "${TARBALL}" -C "${OUTPUT_DIR}" .
rm -rf "${OUTPUT_DIR}"
echo "Done: ${TARBALL}"
