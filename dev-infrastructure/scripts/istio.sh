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

export NEWVERSION="$TARGET_VERSION"
export OLDVERSION="$CURRENT_VERSION"
echo "********** Istio UpGrade Started with version ${NEWVERSION} **************"

# Use revision tag to upgrade istio. 
istioctl tag set "$TAG" --revision "${OLDVERSION}" --istioNamespace aks-istio-system --overwrite
istioctl tag set prod-canary --revision "${NEWVERSION}" --istioNamespace aks-istio-system --overwrite

# Get the namespaces with the label istio.io/rev=$TAG or istio.io/rev=$OLDVERSION(If istio upgrade has never run before, the tag will be the old istio version)
export namespaces=$(kubectl get namespaces --selector=istio.io/rev="$TAG" -o jsonpath='{.items[*].metadata.name}' | xargs -n1 echo)

istioctl tag set "$TAG" --revision "${NEWVERSION}" --istioNamespace aks-istio-system --overwrite

for ns in $namespaces; do
    pods=$(kubectl get pods -n "$ns")
    for pod in $pods; do
        pod_name=$(kubectl get pods -n "$ns" -o jsonpath='{.items[*].metadata.name}')
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

istioctl tag remove prod-canary --istioNamespace aks-istio-system 

echo "********** ISTIO Upgrade Finished**************"