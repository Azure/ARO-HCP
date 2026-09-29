#!/bin/bash
set -euo pipefail

# TRANSIENT: remove after rollout to all environments (AROSLSRE-2290)
# Cleans up orphaned tracing pods (jaeger, otel-collector, lgtm) left behind
# after PR #7072 removed the observability/tracing pipeline.

NAMESPACE="observability"

if kubectl get namespace "${NAMESPACE}" &>/dev/null; then
  echo "Deleting orphaned namespace '${NAMESPACE}'..."
  kubectl delete namespace "${NAMESPACE}" --ignore-not-found
  echo "Namespace cleanup complete."
else
  echo "Namespace '${NAMESPACE}' does not exist. Nothing to do."
fi
