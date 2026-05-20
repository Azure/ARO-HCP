#!/bin/bash

set -euo pipefail

CLUSTER_NAME=$1
shift
# Port forward specification is expected to be in the format of "namespace/service/localPort/remotePort"
PORT_FORWARD_SPEC="$1"
shift
NAMESPACE=$(echo "$PORT_FORWARD_SPEC" | cut -d'/' -f1)
SERVICE_NAME=$(echo "$PORT_FORWARD_SPEC" | cut -d'/' -f2)
LOCAL_PORT=$(echo "$PORT_FORWARD_SPEC" | cut -d'/' -f3)
REMOTE_PORT=$(echo "$PORT_FORWARD_SPEC" | cut -d'/' -f4)

# Get Kubeconfig
export KUBECONFIG=$(mktemp)
${HCPCTL} sc breakglass "${CLUSTER_NAME}" --output "${KUBECONFIG}" --no-shell

# Start port-forward in background
kubectl port-forward -n "$NAMESPACE" svc/"$SERVICE_NAME" "$LOCAL_PORT":"$REMOTE_PORT" &
PORT_FORWARD_PID=$!

# Ensure port-forward is killed on exit
cleanup() {
	rm -rf "${KUBECONFIG}"
	echo "Stopping port-forward (PID $PORT_FORWARD_PID)..."
	if kill "$PORT_FORWARD_PID" 2>/dev/null; then
		wait "$PORT_FORWARD_PID" 2>/dev/null || true
	fi
}
trap cleanup EXIT INT TERM

echo "Port-forward established: localhost:$LOCAL_PORT -> $SERVICE_NAME.$NAMESPACE:$REMOTE_PORT"
echo "PID: $PORT_FORWARD_PID"

# Wait for port-forward to be ready
for i in $(seq 1 30); do
	if curl --silent --output /dev/null --max-time 1 "http://localhost:$LOCAL_PORT" 2>/dev/null; then
		break
	fi
	if ! kill -0 "$PORT_FORWARD_PID" 2>/dev/null; then
		echo "Port-forward process died unexpectedly"
		exit 1
	fi
	if [ "$i" -eq 30 ]; then
		echo "Timed out waiting for port-forward on localhost:$LOCAL_PORT"
		exit 1
	fi
	sleep 1
done

echo "Running command: $*"

"$@"
