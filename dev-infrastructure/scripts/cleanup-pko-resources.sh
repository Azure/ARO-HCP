#!/bin/bash

# Cleanup script for Package Operator custom resources and CRDs.
#
# Delete all CRs in the package-operator.run API group and rely on ownerRef
# cascading deletion (blockOwnerDeletion: true)
# to clean up child resources. Once all CRs are gone, remove the CRDs.
#
# Idempotent — safe to run on clusters that never had PKO installed.
# Best-effort — logs errors but always exits 0 so it never blocks rollouts.
#
# Tracks: AROSLSRE-782

set -o nounset
set -o pipefail

ERRORS=0
TIMEOUT="${PKO_CLEANUP_TIMEOUT:-120s}"

log_error() {
  echo "ERROR: $*" >&2
  ERRORS=$((ERRORS + 1))
}

echo "=== Package Operator CR + CRD cleanup (best-effort) ==="

# 1. Discover all CRDs in the package-operator.run group, extracting
#    plural name, full API group, and scope from each CRD's spec.
mapfile -t PKO_CRD_INFO < <(
  kubectl get crds -o jsonpath='{range .items[*]}{.metadata.name} {.spec.names.plural} {.spec.group} {.spec.scope}{"\n"}{end}' \
    2>/dev/null | grep ' \(.*\.\)\{0,1\}package-operator\.run ' || true
)

if [[ ${#PKO_CRD_INFO[@]} -eq 0 ]]; then
  echo "No package-operator.run CRDs found. Nothing to do."
  exit 0
fi

echo "Found ${#PKO_CRD_INFO[@]} CRD(s):"
printf '  %s\n' "${PKO_CRD_INFO[@]}"

delete_crs() {
  local plural="$1" group="$2" scope="$3"
  local resource="${plural}.${group}"

  echo ""
  echo "--- Deleting all ${resource} CRs (${scope}) ---"
  if [[ "${scope}" == "Namespaced" ]]; then
    kubectl delete "${resource}" --all-namespaces --all --timeout="${TIMEOUT}" 2>/dev/null || log_error "Failed to delete ${resource} (namespaced)"
  else
    kubectl delete "${resource}" --all --timeout="${TIMEOUT}" 2>/dev/null || log_error "Failed to delete ${resource} (cluster-scoped)"
  fi
}

count_crs() {
  local plural="$1" group="$2" scope="$3"
  local resource="${plural}.${group}"
  local output

  if [[ "${scope}" == "Namespaced" ]]; then
    output=$(kubectl get "${resource}" --all-namespaces -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null) || {
      log_error "kubectl get ${resource} --all-namespaces failed"
      echo 0
      return
    }
  else
    output=$(kubectl get "${resource}" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null) || {
      log_error "kubectl get ${resource} failed"
      echo 0
      return
    }
  fi

  if [[ -z "${output}" ]]; then
    echo 0
  else
    echo "${output}" | wc -l
  fi
}

strip_finalizers() {
  local plural="$1" group="$2" scope="$3"
  local resource="${plural}.${group}"

  if [[ "${scope}" == "Namespaced" ]]; then
    while IFS= read -r entry; do
      [[ -z "${entry}" ]] && continue
      local ns name
      ns=$(echo "${entry}" | cut -d' ' -f1)
      name=$(echo "${entry}" | cut -d' ' -f2)
      echo "  Patching finalizers on ${resource}/${name} -n ${ns}"
      kubectl patch "${resource}" "${name}" -n "${ns}" \
        --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || log_error "Failed to patch finalizers on ${resource}/${name} -n ${ns}"
    done < <(kubectl get "${resource}" --all-namespaces \
               -o jsonpath='{range .items[*]}{.metadata.namespace} {.metadata.name}{"\n"}{end}' 2>/dev/null || true)
  else
    while IFS= read -r name; do
      [[ -z "${name}" ]] && continue
      echo "  Patching finalizers on ${resource}/${name}"
      kubectl patch "${resource}" "${name}" \
        --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || log_error "Failed to patch finalizers on ${resource}/${name}"
    done < <(kubectl get "${resource}" \
               -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
  fi
}

# 2. Delete all CRs for each CRD
for info in "${PKO_CRD_INFO[@]}"; do
  read -r _crd plural group scope <<< "${info}"
  delete_crs "${plural}" "${group}" "${scope}"
done

# 3. Wait for cascading deletion — poll until no CRs remain or we time out
echo ""
echo "Waiting for cascading deletion to complete..."
max_wait=180
elapsed=0
remaining=1
while [[ $elapsed -lt $max_wait ]]; do
  remaining=0
  for info in "${PKO_CRD_INFO[@]}"; do
    read -r _crd plural group scope <<< "${info}"
    count=$(count_crs "${plural}" "${group}" "${scope}")
    remaining=$((remaining + count))
  done

  if [[ $remaining -eq 0 ]]; then
    echo "All package-operator CRs have been deleted."
    break
  fi

  echo "  ${remaining} CR(s) still remaining, waiting... (${elapsed}s / ${max_wait}s)"
  sleep 10
  elapsed=$((elapsed + 10))
done

# 4. Force-remove stuck resources by patching out finalizers.
#    The PKO operator is already uninstalled, so nothing will process
#    these finalizers — we have to strip them ourselves.
if [[ $remaining -gt 0 ]]; then
  echo ""
  echo "WARNING: ${remaining} CR(s) stuck after ${max_wait}s — removing finalizers."
  for info in "${PKO_CRD_INFO[@]}"; do
    read -r _crd plural group scope <<< "${info}"
    strip_finalizers "${plural}" "${group}" "${scope}"
  done

  echo "Waiting for finalizerless resources to terminate..."
  sleep 10

  final_remaining=0
  for info in "${PKO_CRD_INFO[@]}"; do
    read -r _crd plural group scope <<< "${info}"
    count=$(count_crs "${plural}" "${group}" "${scope}")
    final_remaining=$((final_remaining + count))
  done

  if [[ $final_remaining -gt 0 ]]; then
    log_error "${final_remaining} CR(s) still remain after finalizer removal"
  else
    echo "All stuck CRs removed."
  fi
fi

# 5. Delete the CRDs themselves
echo ""
echo "Removing package-operator.run CRDs..."
for info in "${PKO_CRD_INFO[@]}"; do
  read -r crd _plural _group _scope <<< "${info}"
  echo "  Deleting CRD: ${crd}"
  kubectl delete crd "${crd}" --timeout="${TIMEOUT}" --ignore-not-found 2>/dev/null || log_error "Failed to delete CRD ${crd}"
done

echo ""
if [[ $ERRORS -gt 0 ]]; then
  echo "=== PKO cleanup completed with ${ERRORS} error(s) (best-effort, not blocking rollout) ==="
else
  echo "=== PKO resource cleanup complete ==="
fi

exit 0
