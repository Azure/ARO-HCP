#!/bin/bash

set -euo pipefail

echo "********** Download istioctl **************"
# Determines the operating system.
OS="${TARGET_OS:-$(uname)}"
if [ "${OS}" = "Darwin" ] ; then
  OSEXT="osx"
else
  OSEXT="linux"
fi
# Determine arch
LOCAL_ARCH=$(uname -m)
case "${LOCAL_ARCH}" in
  x86_64|amd64)
    ISTIO_ARCH=amd64
    ;;
  armv8*|aarch64*|arm64)
    ISTIO_ARCH=arm64
    ;;
  armv*)
    ISTIO_ARCH=armv7
    ;;
  *)
    echo "This system's architecture, ${LOCAL_ARCH}, isn't supported"
    exit 1
    ;;
esac


ISTIO_URL="https://github.com/istio/istio/releases/download/${ISTIOCTL_VERSION}/istio-${ISTIOCTL_VERSION}-${OSEXT}-${ISTIO_ARCH}.tar.gz"
SHA256_URL="https://github.com/istio/istio/releases/download/${ISTIOCTL_VERSION}/istio-${ISTIOCTL_VERSION}-${OSEXT}-${ISTIO_ARCH}.tar.gz.sha256"
# Download the Istioctl binary
wget -q "$ISTIO_URL" -O istio-"${ISTIOCTL_VERSION}"-${OSEXT}-${ISTIO_ARCH}.tar.gz

# Download the SHA-256 checksum file
wget -q "$SHA256_URL" -O istio-"${ISTIOCTL_VERSION}"-${OSEXT}-${ISTIO_ARCH}.tar.gz.sha256

# Verify the downloaded file
sha256sum -c istio-"${ISTIOCTL_VERSION}"-${OSEXT}-${ISTIO_ARCH}.tar.gz.sha256

# Check the result of the verification
if sha256sum -c istio-"${ISTIOCTL_VERSION}"-${OSEXT}-${ISTIO_ARCH}.tar.gz.sha256; then
    echo "Verification successful: The file is intact."
else
    echo "Verification failed: The file is corrupted."
    exit 1
fi

tar -xzf istio-"${ISTIOCTL_VERSION}"-${OSEXT}-${ISTIO_ARCH}.tar.gz
cd istio-"${ISTIOCTL_VERSION}"
export PATH=$PWD/bin:$PATH
echo "=========================================================================="

#
# Create the tag if it does not exist yet
#

ISTIO_NAMESPACE="aks-istio-system"

echo "********** ISTIO Upgrade **************"
# Followed this guide for istio upgrade https://learn.microsoft.com/en-us/azure/aks/istio-upgrade
# To upgrade or rollback, change the targetVersion to the desire version, and version to the current version.
if [[ -z "$TARGET_VERSION" ]]; then
    echo "Istio is using Target Version. Exiting script."
    exit 1
fi

NEWVERSION="$TARGET_VERSION"
echo "********** Istio Upgrade Started with version ${NEWVERSION} **************"

istioctl tag set "$TAG" --revision "${NEWVERSION}" --istioNamespace ${ISTIO_NAMESPACE} --overwrite

for namespace in $(kubectl get namespaces --selector=istio.io/rev="$TAG" -o jsonpath='{.items[*].metadata.name}'); do
    pods="$(kubectl get pods --namespace "${namespace}" -o json)"
    for pod in $(jq <<<"${pods}" --raw-output --arg NEWVERSION "${NEWVERSION}" '.items[] | select(.metadata.annotations["sidecar.istio.io/status"] | fromjson.revision != $NEWVERSION) | .metadata.name'); do
        owner_kind=$(jq <<<"${pods}" --raw-output --arg NAME "${pod}" '.items[] | select(.metadata.name == $NAME) | .metadata.ownerReferences[0].kind')
        owner_name=$(jq <<<"${pods}" --raw-output --arg NAME "${pod}" '.items[] | select(.metadata.name == $NAME) | .metadata.ownerReferences[0].name')        
            case "$owner_kind" in
                "ReplicaSet")
                    deployment=$(kubectl get replicaset "$owner_name" -n "$namespace" -o jsonpath='{.metadata.ownerReferences[0].name}')
                    if [[ -n "$deployment" ]]; then
                        kubectl rollout restart deployment "$deployment" -n "$namespace"
                        continue 2
                    else
                        kubectl delete pod "$pod" -n "$namespace"
                    fi
                ;;
                "StatefulSet")
                    deployment=$(kubectl get replicaset "$owner_name" -n "$namespace" -o jsonpath='{.metadata.ownerReferences[0].name}')
                    kubectl rollout restart deployment "$deployment" -n "$namespace"
                    continue 2
                ;;
                *)
                    # Don't do anything for (Cron)Job, or no owner pod for now.
                ;;
            esac
        # etc
    done
done

echo "********** ISTIO Upgrade Finished**************"
