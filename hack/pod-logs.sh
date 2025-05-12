#!/bin/bash

# * shows a section of pod logs
# * ignores istio init container
#   - istio-init
#   - istio-proxy

POD_NAME="$1"
NAMESPACE="$2"
NR_OF_LINES=${3:-100}

if [[ -z "$POD_NAME" || -z "$NAMESPACE" ]]; then
  echo "Usage: $0 <pod-name> <namespace>"
  exit 1
fi

echo "==== Logs for pod: $POD_NAME in namespace: $NAMESPACE ===="

# Get the list of init containers, excluding Istio-related ones
INIT_CONTAINERS=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.initContainers[*].name}' | tr ' ' '\n' | grep -vE 'istio-init|istio-proxy')

# Show logs for each init container
for CONTAINER in $INIT_CONTAINERS; do
  echo "==== Logs for init container: $CONTAINER ===="
  kubectl logs "$POD_NAME" -n "$NAMESPACE" -c "$CONTAINER"
done

# Get the list of main containers
MAIN_CONTAINERS=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.containers[*].name}' | tr ' ' '\n')

if [[ -z "$MAIN_CONTAINERS" ]]; then
  echo "No main containers found."
else
  # Show the last 100 lines of logs for each main container that is in a proper state
  for CONTAINER in $MAIN_CONTAINERS; do
    STATUS=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath="{.status.containerStatuses[?(@.name=='$CONTAINER')].state.running}")
    if [[ -n "$STATUS" ]]; then
        echo
        echo "==== Last ${NR_OF_LINES} lines of logs for main container: $CONTAINER ===="
        kubectl logs "$POD_NAME" -n "$NAMESPACE" -c "$CONTAINER" --tail="${NR_OF_LINES}"
    else
        echo
        echo "==== Skipping logs for main container: $CONTAINER (not running)"
    fi
  done
fi
