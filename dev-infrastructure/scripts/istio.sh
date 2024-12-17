#!/bin/bash

set -euxo pipefail

echo "********** ISTIO Upgrade Started **************"
# Followed this guide for istio upgrade https://learn.microsoft.com/en-us/azure/aks/istio-upgrade
# To upgrade or rollback, change the targetVersion to the desire version, and version to the current version.
if [[ -z "$TARGET_VERSION" ]]; then
    echo "Istio is using Target Version. Exiting script."
    exit 0
fi

echo "********** Download istioctl**************"
ISTIO_URL="${ISTIOCTL_URL}/${ISTIOCTL_VERSION}/istio-${ISTIOCTL_VERSION}-linux-amd64.tar.gz"
SHA256_URL="${ISTIOCTL_URL}/${ISTIOCTL_VERSION}/istio-${ISTIOCTL_VERSION}-linux-amd64.tar.gz.sha256"
# Download the Istioctl binary
wget "$ISTIO_URL" -O istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz

# Download the SHA-256 checksum file
wget "$SHA256_URL" -O istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz.sha256

# Verify the downloaded file
sha256sum -c istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz.sha256

# Check the result of the verification
if sha256sum -c istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz.sha256; then
    echo "Verification successful: The file is intact."
else
    echo "Verification failed: The file is corrupted."
    exit 1
fi

tar -xzf istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz
cd istio-"${ISTIOCTL_VERSION}"
export PATH=$PWD/bin:$PATH
echo "=========================================================================="

NEWVERSION="$TARGET_VERSION"
echo "********** Istio UpGrade Started with version ${NEWVERSION} **************"

istioctl tag set "$TAG" --revision "${NEWVERSION}" --istioNamespace aks-istio-system --overwrite
# Get the namespaces with the label istio.io/rev=$TAG
namespaces=$(kubectl get namespaces --selector=istio.io/rev="$TAG" -o jsonpath='{.items[*].metadata.name}')

for ns in $namespaces; do
    pods=$(kubectl get pods -n "$ns" -o jsonpath='{.items[*].metadata.name}')
    for pod_name in $pods; do
        istio_version=$(kubectl get pod "$pod_name" -n "$ns" -o jsonpath='{.metadata.annotations.sidecar\.istio\.io/status}' | grep -oP '(?<="revision":")[^"]*')
        if [[ "$istio_version" != "$NEWVERSION" ]]; then
            owner_kind=$(kubectl get pod "$pod_name" -n "$ns" -o jsonpath='{.metadata.ownerReferences[0].kind}')
            owner_name=$(kubectl get pod "$pod_name" -n "$ns" -o jsonpath='{.metadata.ownerReferences[0].name}')

            case "$owner_kind" in
                "ReplicaSet")
                    deployment=$(kubectl get replicaset "$owner_name" -n "$ns" -o jsonpath='{.metadata.ownerReferences[0].name}')
                    if [[ -n "$deployment" ]]; then
                        kubectl rollout restart deployment "$deployment" -n "$ns"
                    else
                        kubectl delete pod "$pod_name" -n "$ns"
                    fi
                ;;
                "StatefulSet")
                    deployment=$(kubectl get replicaset "$owner_name" -n "$ns" -o jsonpath='{.metadata.ownerReferences[0].name}')
                    kubectl rollout restart deployment "$deployment" -n "$ns"
                ;;
                *)
                    # Don't do anything for (Cron)Job, or no owner pod for now.
                ;;
            esac

        fi
    done
done

echo "********** ISTIO Upgrade Finished**************"