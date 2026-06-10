#!/bin/bash
set -euo pipefail

# Inputs via environment variables:
#   ACR_NAME - ACR registry name (without .azurecr.io suffix)
#   TIMEOUT  - (optional) Total timeout in seconds, default 1500

TIMEOUT=${TIMEOUT:-1500}
ACR_NAME="${ACR_NAME:?ACR_NAME is required}"

# --- Phase 1: Node readiness ---
echo "Phase 1: Waiting for all nodes to be Ready (timeout: ${TIMEOUT}s)..."

if ! kubectl wait --for=condition=Ready node --all --timeout="${TIMEOUT}s"; then
    echo "TIMEOUT: nodes not ready after ${TIMEOUT}s"
    kubectl get nodes -o wide
    exit 1
fi

# --- Phase 2: Probe pod ---
echo "Phase 2: Verifying pod scheduling, networking and image pull..."

PROBE_IMAGE="${ACR_NAME}.azurecr.io/oss/kubernetes/pause@sha256:a67d781a5a51290a56f6fb603b8ac9509abce8948d5a52ff3e02e8669a83180d"

kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: node-readiness-probe
  namespace: default
spec:
  containers:
  - name: pause
    image: ${PROBE_IMAGE}
    resources: {}
  restartPolicy: Never
  tolerations:
  - operator: Exists
EOF

echo "Waiting up to ${TIMEOUT}s for probe pod to be Ready..."
if ! kubectl wait --for=condition=Ready pod/node-readiness-probe -n default --timeout="${TIMEOUT}s"; then
    echo "TIMEOUT: probe pod not ready"
    kubectl describe pod node-readiness-probe -n default
    exit 1
fi

echo "Node readiness gate passed."
