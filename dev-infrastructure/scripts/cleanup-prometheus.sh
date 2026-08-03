#!/bin/bash
set -euo pipefail

# Cleans up the prometheus-operator deployment from SVC clusters after
# migrating to Azure Monitor. Deletes CRs, namespace, cluster-scoped
# resources, and CRDs. Skips anything already absent.

delete_if_exists() {
    local resource="$1"
    local name="$2"
    if kubectl get "$resource" "$name" &>/dev/null; then
        echo "Deleting $resource/$name ..."
        kubectl delete "$resource" "$name"
    else
        echo "Skipping $resource/$name (not found)"
    fi
}

delete_ns_if_exists() {
    local ns="$1"
    if kubectl get namespace "$ns" &>/dev/null; then
        echo "Deleting namespace $ns ..."
        kubectl delete namespace "$ns"
    else
        echo "Skipping namespace $ns (not found)"
    fi
}

echo "=== Cleaning up prometheus-operator from SVC cluster ==="

# 1. Delete the prometheus namespace (cascades all namespace-scoped resources)
delete_ns_if_exists "prometheus"

# 2. Delete cluster-scoped resources left by the Helm chart
delete_if_exists clusterrole "prometheus-operator"
delete_if_exists clusterrole "prometheus"
delete_if_exists clusterrole "prometheus-admission"
delete_if_exists clusterrole "arohcp-monitor-kube-state-metrics"

delete_if_exists clusterrolebinding "prometheus-operator"
delete_if_exists clusterrolebinding "prometheus"
delete_if_exists clusterrolebinding "prometheus-admission"

delete_if_exists validatingwebhookconfiguration "prometheus-admission"
delete_if_exists mutatingwebhookconfiguration "prometheus-admission"

# 3. Delete CRDs installed by kube-prometheus-stack
delete_if_exists crd "alertmanagerconfigs.monitoring.coreos.com"
delete_if_exists crd "alertmanagers.monitoring.coreos.com"
delete_if_exists crd "podmonitors.monitoring.coreos.com"
delete_if_exists crd "probes.monitoring.coreos.com"
delete_if_exists crd "prometheusagents.monitoring.coreos.com"
delete_if_exists crd "prometheuses.monitoring.coreos.com"
delete_if_exists crd "prometheusrules.monitoring.coreos.com"
delete_if_exists crd "scrapeconfigs.monitoring.coreos.com"
delete_if_exists crd "servicemonitors.monitoring.coreos.com"
delete_if_exists crd "thanosrulers.monitoring.coreos.com"

echo "=== Prometheus cleanup complete ==="
