#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage: [DRY_RUN=true] ./delete-custom-resources.sh
#
# Deletes all Route Monitor Operator custom resource instances
# This must be done before uninstalling the Helm release to ensure clean deletion

DRY_RUN="${DRY_RUN:-false}"

echo "🧹 Starting cleanup of RMO custom resources"
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

# Function to safely delete resources
safe_delete() {
    local resource_type="$1"
    local namespace="${2:-}"
    local description="$3"

    log STEP "Deleting $description..."

    # Build kubectl command
    local cmd="kubectl get $resource_type"
    if [[ -n "$namespace" ]]; then
        cmd="$cmd -n $namespace"
    else
        cmd="$cmd --all-namespaces"
    fi

    # Get resources
    local resources
    if resources=$(eval "$cmd -o name 2>/dev/null"); then
        if [[ -z "$resources" ]]; then
            log INFO "No $description found"
            return 0
        fi

        while IFS= read -r resource; do
            [[ -z "$resource" ]] && continue

            if [[ "$DRY_RUN" == "true" ]]; then
                log INFO "[DRY RUN] Would delete: $resource"
            else
                log INFO "Deleting: $resource"
                local delete_cmd="kubectl delete $resource"
                if [[ -n "$namespace" ]]; then
                    delete_cmd="$delete_cmd -n $namespace"
                fi

                if eval "$delete_cmd --ignore-not-found=true --timeout=60s 2>/dev/null"; then
                    log SUCCESS "Deleted: $resource"
                else
                    log WARN "Failed to delete: $resource (may already be gone)"
                fi
            fi
        done <<< "$resources"
    else
        log INFO "No $description found (CRD may not exist)"
    fi
}

# Step 1: Delete ClusterUrlMonitor instances
log STEP "Step 1: Deleting ClusterUrlMonitor instances"
safe_delete "clusterurlmonitors.monitoring.openshift.io" "" "ClusterUrlMonitor instances"

# Step 2: Delete RouteMonitor instances
log STEP "Step 2: Deleting RouteMonitor instances"
safe_delete "routemonitors.monitoring.openshift.io" "" "RouteMonitor instances"

# Step 3: Delete ServiceMonitor instances (monitoring.rhobs)
log STEP "Step 3: Deleting ServiceMonitor (monitoring.rhobs) instances"
safe_delete "servicemonitors.monitoring.rhobs" "" "ServiceMonitor (monitoring.rhobs) instances"

# Step 4: Wait for resources to be fully deleted
if [[ "$DRY_RUN" != "true" ]]; then
    log INFO "Waiting for resources to be fully deleted..."
    sleep 5

    # Verify deletion
    remaining_clusterurl=$(kubectl get clusterurlmonitors.monitoring.openshift.io --all-namespaces -o name 2>/dev/null | wc -l || echo "0")
    remaining_route=$(kubectl get routemonitors.monitoring.openshift.io --all-namespaces -o name 2>/dev/null | wc -l || echo "0")
    remaining_svcmon=$(kubectl get servicemonitors.monitoring.rhobs --all-namespaces -o name 2>/dev/null | wc -l || echo "0")

    if [[ "$remaining_clusterurl" -eq 0 ]] && [[ "$remaining_route" -eq 0 ]] && [[ "$remaining_svcmon" -eq 0 ]]; then
        log SUCCESS "All custom resources have been deleted"
    else
        log WARN "$remaining_clusterurl ClusterUrlMonitor(s), $remaining_route RouteMonitor(s), and $remaining_svcmon ServiceMonitor(s) still remain"
    fi
fi

log SUCCESS "RMO custom resources cleanup completed"
