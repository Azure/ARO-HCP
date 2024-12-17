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
ISTIO_URL="https://github.com/istio/istio/releases/download/${ISTIOCTL_VERSION}/istio-${ISTIOCTL_VERSION}-linux-amd64.tar.gz"
SHA256_URL="https://github.com/istio/istio/releases/download/${ISTIOCTL_VERSION}/istio-${ISTIOCTL_VERSION}-linux-amd64.tar.gz.sha256"
# Download the Istioctl binary
wget $ISTIO_URL -O istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz

# Download the SHA-256 checksum file
wget $SHA256_URL -O istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz.sha256

# Verify the downloaded file
sha256sum -c istio-"${ISTIOCTL_VERSION}"-linux-amd64.tar.gz.sha256

# Check the result of the verification
if [ $? -eq 0 ]; then
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
istioctl tag set "$TAG" --revision "${OLDVERSION}" --istioNamespace aks-istio-system
istioctl tag set prod-canary --revision "${NEWVERSION}" --istioNamespace aks-istio-system

# Get the namespaces with the label istio.io/rev=$TAG or istio.io/rev=$OLDVERSION(If istio upgrade has never run before, the tag will be the old istio version)
export namespaces=$(kubectl get namespaces --selector=istio.io/rev="$OLDVERSION" -o jsonpath='{.items[*].metadata.name}' | xargs -n1 echo; kubectl get namespaces --selector=istio.io/rev="$TAG" -o jsonpath='{.items[*].metadata.name}' | xargs -n1 echo)

# Label the current namespace the TAG
for ns in $namespaces; do
    kubectl label namespace "$ns" default istio.io/rev="$TAG" --overwrite
done

istioctl tag set "$TAG" --revision "${NEWVERSION}" --istioNamespace aks-istio-system --overwrite

# Restart all pods 
kubectl get pods --all-namespaces -l istio.io/rev="$TAG" -o jsonpath='{range .items[*]}{.metadata.namespace}{" "}{.metadata.name}{"\n"}{end}' | xargs -n2 sh -c "kubectl delete pod -n $0 $1"

istioctl tag remove prod-canary --istioNamespace aks-istio-system 

echo "********** ISTIO Upgrade Finished**************"