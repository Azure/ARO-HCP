#!/bin/sh
#
# E X P E R I M E N T A L   -   N O T   I N T E N D E D   F O R   P R O D U C T I O N
# Use at your own risk. This script does not do sanity checks and only partial error handling.
#
# This is an experiment script that creates a kubeconfig for an HCP using the sre-break-glass signer.
# This script is intended to be run towards the management cluster that hosts the HCP we want to access.
#
# The resulting kubeconfig will grant membership in the sre-group group within the HCP.
# An ACM policy grants that group access to the sre-role, which is a simple dummy role that allows listing all namespace.
#
# Example usage:
# ./hcp-sre-breakglass.sh <cs-cluster-id>
#

set -euo pipefail

SUBJECT="/CN=system:sre-break-glass:${USER}/O=sre-group"

CS_CLUSTER_ID=$1
HOSTED_CLUSTER_CR=$(kubectl get hostedcluster -A -l "api.openshift.com/id=${CS_CLUSTER_ID}" -o yaml | yq -r '.items[0]')
HCP_NAMESPACE=$(echo "${HOSTED_CLUSTER_CR}" | yq '.metadata.namespace + "-" + (.metadata.labels["api.openshift.com/name"] | tostring)')
HCP_API_SERVER=$(echo "${HOSTED_CLUSTER_CR}" | yq '.status.controlPlaneEndpoint.host')

# download the API server CA from the HCP_API_SERVER_URL
echo "Downloading the API server CA from ${HCP_API_SERVER}"
HCP_ROOT_CA_CRT=$(openssl s_client -servername "${HCP_API_SERVER}" -connect "${HCP_API_SERVER}:443" </dev/null 2>/dev/null | sed -ne '/-BEGIN CERTIFICATE-/,/-END CERTIFICATE-/p' | base64 | tr -d '\n')

KEY_FILE="sre-breakglass-${CS_CLUSTER_ID}-${USER}.key"
CSR_NAME="sre-breakglass-${CS_CLUSTER_ID}-${USER}"
KUBECONFIG_FILE="${CSR_NAME}.kubeconfig"

# Ensure cleanup happens on script exit
cleanup() {
  echo "Cleaning up resources..."
  kubectl delete csr "${CSR_NAME}" 2>/dev/null || true
  kubectl delete CertificateSigningRequestApproval "${CSR_NAME}" -n "${HCP_NAMESPACE}" 2>/dev/null || true
  rm -f "${KEY_FILE}" 2>/dev/null || true
}
trap cleanup EXIT

# Generate a private key
echo "Create private key"
openssl genrsa -out "${KEY_FILE}" 2048

# Generate a CSR using the private key and subject
echo "Create CSR with subject ${SUBJECT}"
CSR_BASE64=$(openssl req -new -key "${KEY_FILE}" -subj "${SUBJECT}" | base64 | tr -d '\n')

# Create a Kubernetes CSR manifest
cat <<EOF | kubectl apply -f -
apiVersion: certificates.k8s.io/v1
kind: CertificateSigningRequest
metadata:
  name: ${CSR_NAME}
  labels:
    api.openshift.com/id: $CS_CLUSTER_ID
    api.openshift.com/name: $USER
    api.openshift.com/type: break-glass-credential
spec:
  expirationSeconds: 86353
  request: ${CSR_BASE64}
  signerName: hypershift.openshift.io/${HCP_NAMESPACE}.sre-break-glass
  usages:
  - client auth
  - digital signature
EOF
echo "Kubernetes CSR ${CSR_NAME} applied to the cluster."

cat <<EOF | kubectl apply -f -
apiVersion: certificates.hypershift.openshift.io/v1alpha1
kind: CertificateSigningRequestApproval
metadata:
  creationTimestamp: null
  labels:
    api.openshift.com/id: $CS_CLUSTER_ID
    api.openshift.com/name: $USER
    api.openshift.com/type: break-glass-credential
  name: ${CSR_NAME}
  namespace: ${HCP_NAMESPACE}
EOF
echo "Hypershift CSR Approval ${CSR_NAME} applied to the cluster."

# Wait for the CSR to be approved and signed
echo "Waiting for CSR ${CSR_NAME} to be approved (15s timeout)..."
kubectl wait --for=condition=Approved "csr/${CSR_NAME}" --timeout=15s
kubectl wait --for=jsonpath='{.status.certificate}' --timeout=15s "csr/${CSR_NAME}"
CERTIFICATE=$(kubectl get "csr/${CSR_NAME}" -o jsonpath='{.status.certificate}')
echo "CSR ${CSR_NAME} signed."

# build a kubeconfig from it
cat <<EOF > "${KUBECONFIG_FILE}"
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: ${HCP_ROOT_CA_CRT}
    server: https://${HCP_API_SERVER}:443
  name: ${CS_CLUSTER_ID}
contexts:
- context:
    cluster: ${CS_CLUSTER_ID}
    user: ${CSR_NAME}
  name: ${CSR_NAME}
current-context: ${CSR_NAME}
kind: Config
preferences: {}
users:
- name: ${CSR_NAME}
  user:
    client-certificate-data: ${CERTIFICATE}
    client-key-data: $(base64 -i "${KEY_FILE}" -w 0)
EOF
echo "Kubeconfig ${KUBECONFIG_FILE} created."
echo "export KUBECONFIG=$(realpath "${KUBECONFIG_FILE}")"
