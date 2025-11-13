#!/bin/bash

set -euo pipefail

# Get Kubeconfig
KUBECONFIG=$(mktemp)
${HCPCTL} sc breakglass "${SVC_CLUSTER}" --output "${KUBECONFIG}" --no-shell

# Start port-forward in background
kubectl port-forward -n "$NAMESPACE" svc/"$SERVICE_NAME" "$LOCAL_PORT":"$REMOTE_PORT" &
PORT_FORWARD_PID=$!

# Ensure port-forward is killed on exit
cleanup() {
	rm "${KUBECONFIG}"
	echo "Stopping port-forward (PID $PORT_FORWARD_PID)..."
	if kill "$PORT_FORWARD_PID" 2>/dev/null; then
		wait "$PORT_FORWARD_PID" 2>/dev/null || true
	fi
}
trap cleanup EXIT

echo "Port-forward established: localhost:$LOCAL_PORT -> $SERVICE_NAME.$NAMESPACE:$REMOTE_PORT"
echo "PID: $PORT_FORWARD_PID"

# Wait a moment for port-forward to be ready
sleep 2

# Test the connection
echo "Run Test"

${CLI_BINARY} hello-world \
		--ga-auth-cert-kv "${GA_AUTH_CERT_KV}" \
		--ga-auth-cert-secret "${GA_AUTH_CERT_SECRET}" \
		--ga-auth-tenant-id "${GA_AUTH_TENANT_ID}" \
		--ga-auth-client-id "${GA_AUTH_CLIENT_ID}" \
		--ga-auth-scopes "${GA_AUTH_SCOPES}" \
		--host "${ADMIN_API_HOST}" \
		--admin-api-endpoint "${ADMIN_API_ENDPOINT}" \
		--insecure-skip-verify