#!/bin/bash

set -euxo pipefail

echo "********** ISTIO Upgrade Started **************"
# Get the current istio and check if it match target version
export CURRENTVERSION=$(kubectl get pods -n aks-istio-system | tail -n +2 | tail -n 1 | cut -d '-' -f 2,3,4)
if [ "$CURRENTVERSION" == "$TARGET_VERSION" ] || [ -z "$TARGET_VERSION" ]; then
    echo "Istio is using Target Version. Exiting script."
    exit 0
fi

echo "********** Download istioctl**************"
curl -L "{$ISTIOCTL_URL}" | ISTIO_VERSION="${ISTIOCTL_VERSION}" TARGET_ARCH=x86_64 sh -
cd istio-"${ISTIOCTL_VERSION}"
export PATH=$PWD/bin:$PATH
echo "=========================================================================="

export NEWVERSION="$TARGET_VERSION"
echo "********** Istio UpGrade Started with version ${NEWVERSION} **************"

# Overwrite the tag with new istio version
istioctl tag set "$TAG" --revision "${NEWVERSION}" -i aks-istio-system --overwrite

# Get the namespaces with the label istio.io/rev=$TAG
export namespaces=$(kubectl get namespaces --selector=istio.io/rev="$TAG") 
for ns in $namespaces; do
    # Get all deployments in the namespace
    deployments=$(kubectl get deployments -n "$ns" -o jsonpath='{.items[*].metadata.name}')
    # Iterate over each deployment and restart it
    for deployment in $deployments; do
        kubectl rollout restart deployment "$deployment" -n "$ns"
    done
done

echo "********** ISTIO Upgrade Finished**************"