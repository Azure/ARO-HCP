#!/bin/bash
set -euo pipefail

NAMESPACE="default-sc"

# Prepare the namespace before Helm creates pods that mount its root CA.
kubectl create namespace "${NAMESPACE}" --request-timeout=10s --dry-run=client -o yaml |
    kubectl apply --request-timeout=10s -f -

echo "Waiting for ${NAMESPACE}/kube-root-ca.crt to be published..."
kubectl wait --request-timeout=10s --namespace="${NAMESPACE}" \
    --for=create configmap/kube-root-ca.crt --timeout=2m

# An existing ConfigMap is not ready until the publisher has populated ca.crt.
kubectl wait --request-timeout=10s --namespace="${NAMESPACE}" \
    --for=jsonpath='{.data.ca\.crt}' configmap/kube-root-ca.crt --timeout=2m

echo "Namespace '${NAMESPACE}' has a published root CA."
