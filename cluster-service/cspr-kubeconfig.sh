#!/bin/bash

NAMESPACE=$1
SERVICE_ACCOUNT=$2
OUTPUT_FILE=$3

# Validate input parameters
if [ -z "$NAMESPACE" ] || [ -z "$SERVICE_ACCOUNT" ] || [ -z "$OUTPUT_FILE" ]; then
    echo "Usage: $0 <namespace> <service_account> <output_file>"
    echo "Example: $0 cluster-service-admin cluster-service-mgmt ./cspr-kubeconfig"
    exit 1
fi

# Get the service account token secret name
TOKEN_SECRET="${SERVICE_ACCOUNT}-token"

# Extract the token from the secret
echo "Extracting token for service account '$SERVICE_ACCOUNT' in namespace '$NAMESPACE'..."
TOKEN=$(kubectl get secret "$TOKEN_SECRET" -n "$NAMESPACE" -o jsonpath='{.data.token}' | base64 --decode)

if [ -z "$TOKEN" ]; then
    echo "Error: Could not retrieve token from secret '$TOKEN_SECRET' in namespace '$NAMESPACE'"
    exit 1
fi

# Get the cluster CA certificate
echo "Extracting cluster CA certificate..."
CA_CERT=$(kubectl get secret "$TOKEN_SECRET" -n "$NAMESPACE" -o jsonpath='{.data.ca\.crt}')

if [ -z "$CA_CERT" ]; then
    echo "Error: Could not retrieve CA certificate from secret '$TOKEN_SECRET'"
    exit 1
fi

# Get the cluster API server URL
echo "Getting cluster API server URL..."
API_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')

if [ -z "$API_SERVER" ]; then
    echo "Error: Could not determine cluster API server URL"
    exit 1
fi

# Generate a unique cluster and context name
CLUSTER_NAME="cspr"
CONTEXT_NAME="${SERVICE_ACCOUNT}-${NAMESPACE}"

# Create the kubeconfig file
echo "Creating kubeconfig file at '$OUTPUT_FILE'..."
cat > "$OUTPUT_FILE" << EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: $CA_CERT
    server: $API_SERVER
  name: $CLUSTER_NAME
contexts:
- context:
    cluster: $CLUSTER_NAME
    namespace: $NAMESPACE
    user: $SERVICE_ACCOUNT
  name: $CONTEXT_NAME
current-context: $CONTEXT_NAME
users:
- name: $SERVICE_ACCOUNT
  user:
    token: $TOKEN
EOF

echo "Kubeconfig file created successfully at '$OUTPUT_FILE'"
echo "Test it with: KUBECONFIG=$OUTPUT_FILE kubectl auth whoami"
