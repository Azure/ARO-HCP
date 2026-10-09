#!/bin/bash
set -euo pipefail

# TRANSIENT: remove after rollout to all environments (AROSLSRE-2290)
# Cleans up orphaned tracing pods (jaeger, otel-collector, lgtm) left behind
# after PR #7072 removed the observability/tracing pipeline.

NAMESPACE="observability"

echo "Listing remaining resources in '${NAMESPACE}' (if any)..."
kubectl get deployments,services,configmaps -n "${NAMESPACE}" --ignore-not-found

echo "Deleting namespace '${NAMESPACE}' (if present)..."
kubectl delete namespace "${NAMESPACE}" --ignore-not-found --timeout=10m

echo "Namespace '${NAMESPACE}' is absent."
