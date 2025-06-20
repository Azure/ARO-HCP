#!/bin/bash

# Temporary hack to get kubeconfig of a hosted cluster until XCMSTRAT-969 is
# fully implemented.
# The script assumes you are logged in mgmt cluster of the hosted cluster.

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

CLUSTER_OCM_NS=$(kubectl get ns -l="api.openshift.com/name=${CLUSTER_NAME}" -o name)
kubectl get secret -n "${CLUSTER_OCM_NS#namespace/}" "${CLUSTER_NAME}-admin-kubeconfig" -o json | jq .data.kubeconfig -r | base64 -d > "${CLUSTER_NAME}.kubeconfig"
