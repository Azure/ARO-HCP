#!/bin/bash

set -euo pipefail
ISTIO_NAMESPACE="aks-istio-system"
echo "********** Check istio is up and running **************"
ISTIO_PODS_COUNT=$(kubectl get pods -n ${ISTIO_NAMESPACE} -l istio.io/rev="${TARGET_VERSION}" --field-selector=status.phase=Running --no-headers | wc -l)
if [[ $ISTIO_PODS_COUNT -lt 2 ]]; then
    echo "Istio pods are not running, Please check the istio pods"
    exit 1
fi

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
curl -sL "$ISTIO_URL" -o istio-"${ISTIOCTL_VERSION}"-${OSEXT}-${ISTIO_ARCH}.tar.gz

# Download the SHA-256 checksum file
curl -sL "$SHA256_URL" -o istio-"${ISTIOCTL_VERSION}"-${OSEXT}-${ISTIO_ARCH}.tar.gz.sha256

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

echo "********** ISTIO IngressGateway IP Address assignment **************"
ISTIO_IG_ANNOTATIONS="
  service.beta.kubernetes.io/azure-load-balancer-resource-group=${REGION_RESOURCEGROUP}
  service.beta.kubernetes.io/azure-pip-name=${ISTIO_INGRESS_GATEWAY_IP_ADDRESS_NAME}
"
for annotation in $ISTIO_IG_ANNOTATIONS; do
  kubectl annotate --overwrite svc aks-istio-ingressgateway-external \
    "$annotation" \
    -n aks-istio-ingress
done

echo "********** ISTIO Upgrade **************"
# Followed this guide for istio upgrade https://learn.microsoft.com/en-us/azure/aks/istio-upgrade
# To upgrade or rollback, change the targetVersion to the desire version, and version to the current version.
if [[ -z "$TARGET_VERSION" ]]; then
    echo "Target version is not set, Please set the target version"
    exit 1
fi

NEWVERSION="$TARGET_VERSION"
echo "********** Istio Upgrade Started with version ${NEWVERSION} **************"

istioctl tag set "$TAG" --revision "${NEWVERSION}" --istioNamespace ${ISTIO_NAMESPACE} --overwrite
for namespace in $(kubectl get namespaces --selector=istio.io/rev="$TAG" -o jsonpath='{.items[*].metadata.name}'); do
    echo "in namespace $namespace"
    # bare pods
    for pod in $(kubectl get pods --namespace "${namespace}" -o json | jq -r --arg NEWVERSION "${NEWVERSION}" '.items[] | select(.metadata.annotations["sidecar.istio.io/status"] | fromjson.revision != $NEWVERSION) | select(.metadata.ownerReferences | length == 0) | .metadata.name'); do
        echo "recycle pod $pod"
        kubectl delete pod "$pod" -n "$namespace"
    done
    # pods with owners
    currentDeloyment=""
    for owner in $(kubectl get pods --namespace "${namespace}" -o json | jq -r --arg NEWVERSION "${NEWVERSION}" '.items[] | select(.metadata.annotations["sidecar.istio.io/status"] | fromjson.revision != $NEWVERSION) | select(.metadata.ownerReferences) | "\(.metadata.ownerReferences[0].kind)/\(.metadata.ownerReferences[0].name)"' | sort | uniq); do
        echo "process pod owner ${owner}"
        case "$owner" in
            "ReplicaSet"*)
                deployment=$(kubectl get "${owner}" -n "$namespace" -o jsonpath='{.metadata.ownerReferences[0].name}')
                if [[ -n "$deployment" ]] && [[ "$currentDeloyment" != "$deployment" ]]; then
                    currentDeloyment="$deployment"
                    echo "in ReplicaSet restart deployment $deployment"
                    kubectl rollout restart deployment "$deployment" -n "$namespace"
                    kubectl rollout status deployment "${deployment}" -n "$namespace"  
                else
                    echo "in ReplicaSet delete pod $owner"
                    kubectl delete pod "$owner" -n "$namespace"
                fi
            ;;
            "StatefulSet"*)
                echo "restart statefulset $owner"
                kubectl rollout restart "${owner}" -n "$namespace"
                kubectl rollout status "${owner}" -n "$namespace"
            ;;
            *)
                # Don't do anything for (Cron)Job, or no owner pod for now.
            ;;
        esac
        # etc
    done
done

echo "********** ISTIO Upgrade Finished**************"
