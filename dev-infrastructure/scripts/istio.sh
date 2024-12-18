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
CURRENT_TAG_REVISION=$(istioctl tag list --istioNamespace "${ISTIO_NAMESPACE}" -o json | jq --arg tag "${TAG}" '.[] | select(.tag == $tag).revision' -r)

echo "********** Ensure tag ${TAG} exists **************"
if [ -z "$CURRENT_TAG_REVISION" ]; then
    echo "Tag ${TAG} does not exist yet. Creating it with version ${CURRENT_VERSION}"
    istioctl tag set "${TAG}" --revision "${CURRENT_VERSION}" --istioNamespace "${ISTIO_NAMESPACE}"
else
    echo "Tag ${TAG} already exists and refers to version ${CURRENT_TAG_REVISION}"
fi

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

# Get the namespaces with the label istio.io/rev=$TAG
for namespace in $( kubectl get namespaces --selector=istio.io/rev="$TAG" -o jsonpath='{.items[*].metadata.name}' ); do
    for pod in $( kubectl get pods -n "$namespace" -o jsonpath='{.items[*].metadata.name}' ); do
        istio_version=$(kubectl get pod "$pod" -n "$namespace" -o jsonpath='{.metadata.annotations.sidecar\.istio\.io/status}' | grep -oP '(?<="revision":")[^"]*')
        if [[ "$istio_version" != "$NEWVERSION" ]]; then
            owner_kind=$(kubectl get pod "$pod" -n "$namespace" -o jsonpath='{.metadata.ownerReferences[0].kind}')
            owner_name=$(kubectl get pod "$pod" -n "$namespace" -o jsonpath='{.metadata.ownerReferences[0].name}')

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

        fi
    done
done

echo "********** ISTIO Upgrade Finished**************"
