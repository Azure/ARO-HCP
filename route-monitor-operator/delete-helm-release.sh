#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: [DRY_RUN=true] ./delete-helm-release.sh
#
# Uninstalls the route-monitor-operator Helm release
# This must be done after custom resources are deleted

DRY_RUN="${DRY_RUN:-false}"
RELEASE_NAME="route-monitor-operator"
NAMESPACE="openshift-route-monitor-operator"

echo "🧹 Starting cleanup of RMO Helm release"
if [[ "$DRY_RUN" == "true" ]]; then
    echo "🔍 DRY RUN MODE - No resources will actually be deleted"
fi

# Function to log actions
log() {
    local level="$1"
    shift
    local message="$*"
    case "$level" in
        INFO) echo "(i) $message" ;;
        WARN) echo "(w) $message" ;;
        ERROR) echo "(!) $message" ;;
        SUCCESS) echo "(o) $message" ;;
        STEP) echo "(~) $message" ;;
    esac
}

# Step 1: Check if Helm release exists
log STEP "Checking for Helm release: $RELEASE_NAME"

if helm list -n "$NAMESPACE" -o json 2>/dev/null | jq -e ".[] | select(.name == \"$RELEASE_NAME\")" > /dev/null 2>&1; then
    log INFO "Found Helm release: $RELEASE_NAME in namespace: $NAMESPACE"

    if [[ "$DRY_RUN" == "true" ]]; then
        log INFO "[DRY RUN] Would uninstall Helm release: $RELEASE_NAME"
        log INFO "[DRY RUN] Command: helm uninstall $RELEASE_NAME -n $NAMESPACE --wait --timeout=5m"
    else
        log STEP "Uninstalling Helm release: $RELEASE_NAME"
        if helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" --wait --timeout=5m 2>&1; then
            log SUCCESS "Helm release uninstalled: $RELEASE_NAME"
        else
            log ERROR "Failed to uninstall Helm release: $RELEASE_NAME"
            exit 1
        fi
    fi
else
    log INFO "Helm release not found: $RELEASE_NAME (may already be deleted)"
fi

# Step 2: Delete the namespace if it exists and is empty
log STEP "Checking namespace: $NAMESPACE"

if kubectl get namespace "$NAMESPACE" > /dev/null 2>&1; then
    # Check if namespace has any resources left
    resource_count=$(kubectl get all -n "$NAMESPACE" 2>/dev/null | grep -v "^NAME" | wc -l || echo "0")

    if [[ "$resource_count" -eq 0 ]]; then
        if [[ "$DRY_RUN" == "true" ]]; then
            log INFO "[DRY RUN] Would delete empty namespace: $NAMESPACE"
        else
            log STEP "Deleting empty namespace: $NAMESPACE"
            if kubectl delete namespace "$NAMESPACE" --timeout=60s 2>&1; then
                log SUCCESS "Namespace deleted: $NAMESPACE"
            else
                log WARN "Failed to delete namespace: $NAMESPACE (may still have resources)"
            fi
        fi
    else
        log INFO "Namespace $NAMESPACE still has $resource_count resources, skipping deletion"
    fi
else
    log INFO "Namespace not found: $NAMESPACE (may already be deleted)"
fi

# Step 3: Delete RMO CRDs (these should be cleaned up by Helm, but ensure they're gone)
log STEP "Checking for RMO CRDs"

RMO_CRDS=(
    "clusterurlmonitors.monitoring.openshift.io"
    "routemonitors.monitoring.openshift.io"
)

for crd in "${RMO_CRDS[@]}"; do
    if kubectl get crd "$crd" > /dev/null 2>&1; then
        if [[ "$DRY_RUN" == "true" ]]; then
            log INFO "[DRY RUN] Would delete CRD: $crd"
        else
            log INFO "Deleting CRD: $crd"
            if kubectl delete crd "$crd" --timeout=60s 2>&1; then
                log SUCCESS "CRD deleted: $crd"
            else
                log WARN "Failed to delete CRD: $crd"
            fi
        fi
    else
        log INFO "CRD not found: $crd (already deleted)"
    fi
done

log SUCCESS "RMO Helm release cleanup completed"
