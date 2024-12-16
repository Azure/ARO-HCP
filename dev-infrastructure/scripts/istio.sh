#!/bin/bash

set -euxo pipefail

echo "********** ISTIO Upgrade Started **************"
# Followed this guide for istio upgrade https://learn.microsoft.com/en-us/azure/aks/istio-upgrade
# Get the current istio and check if it match target version
export CURRENTVERSION=$(kubectl get pods -n aks-istio-system -o jsonpath='{.items[*].metadata.name}' | tr ' ' '\n' | tail -n +2 | head -n 1 | cut -d '-' -f 2,3,4)
if [[ "$CURRENTVERSION" == "$TARGET_VERSION" ]] || [[ -z "$TARGET_VERSION" ]]; then
    echo "Istio is using Target Version. Exiting script."
    exit 0
fi

echo "********** Download istioctl**************"
curl -L "{$ISTIOCTL_URL}" | ISTIO_VERSION="${ISTIOCTL_VERSION}" TARGET_ARCH=x86_64 sh -
cd istio-"${ISTIOCTL_VERSION}"
export PATH=$PWD/bin:$PATH
echo "=========================================================================="

export NEWVERSION="$TARGET_VERSION"
export OLDVERSION="$CURRENT_VERSION"
echo "********** Istio UpGrade Started with version ${NEWVERSION} **************"

# Use revision tag to upgrade istio. 
istioctl tag set "$TAG" --revision "${OLDVERSION}" --istioNamespace aks-istio-system
istioctl tag set prod-canary --revision "${NEWVERSION}" --istioNamespace aks-istio-system

# Get the namespaces with the label istio.io/rev=$TAG
export namespaces=$(kubectl get namespaces --selector=istio.io/rev="$TAG") 

# Label the current namespace the TAG
for ns in $namespaces; do
    kubectl label "$ns" default istio.io/rev="$TAG" --overwrite
done

istioctl tag set "$TAG" --revision "${NEWVERSION}" --istioNamespace aks-istio-system --overwrite

for ns in $namespaces; do
    # Get all deployments in the namespace
    deployments=$(kubectl get deployments -n "$ns" -o jsonpath='{.items[*].metadata.name}')
    # Iterate over each deployment and restart it
    for deployment in $deployments; do
        kubectl rollout restart deployment "$deployment" -n "$ns"
    done
done

istioctl tag remove prod-canary --istioNamespace aks-istio-system 

echo "********** ISTIO Upgrade Finished**************"