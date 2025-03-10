#!/bin/bash

SCRIPT_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")

DEPLOY_ENV=$1
TARGET_REGISTRY=$2
MODE=$3

TARGET_REGISTRY_URL="${TARGET_REGISTRY}.azurecr.io"

get_image_tag() {
    $SCRIPT_DIR/../../templatize.sh $DEPLOY_ENV | jq ".$1" -r
}

mirror_image() {
    local full_image_ref=$1
    local relative_image_ref=$(echo $full_image_ref | cut -d'/' -f2-)
    if [[ $MODE == "pull" ]]; then
        echo "Pull $full_image_ref with x86 architecture"
        podman pull --arch x86_64 $full_image_ref
    elif [[ $MODE == "push" ]]; then
        echo "Push $full_image_ref to ${TARGET_REGISTRY_URL}/${relative_image_ref}"
        podman tag $full_image_ref ${TARGET_REGISTRY_URL}/${relative_image_ref}
        podman push ${TARGET_REGISTRY_URL}/${relative_image_ref}
    fi
    echo
}

# CS
#mirror_image "quay.io/app-sre/uhc-clusters-service:$(get_image_tag 'clusterService.imageTag')"

# Maestro
mirror_image "quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro:$(get_image_tag 'maestro.imageTag')"

# RP
#mirror_image "arohcpsvcdev.azurecr.io/arohcpbackend:$(get_image_tag 'backend.imageTag')"
#mirror_image "arohcpsvcdev.azurecr.io/arohcpfrontend:$(get_image_tag 'frontend.imageTag')"

# Imagesync
#mirror_image "arohcpsvcdev.azurecr.io/image-sync/component-sync:$(get_image_tag 'imageSync.componentSync.imageTag')"
#mirror_image "arohcpsvcdev.azurecr.io/image-sync/oc-mirror:$(get_image_tag 'imageSync.ocMirror.imageTag')"

# Hypershift
#mirror_image "quay.io/acm-d/rhtap-hypershift-operator:$(get_image_tag 'hypershiftOperator.imageTag')"
