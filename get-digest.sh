#!/bin/bash

if [ "$#" -ne 2 ]
then
    echo "Need ARO_HCP_IMAGE_ACR and REPOSITORY parameters"
    exit 1
fi

aro_hcp_image_acr=${1}
repository=${2}

if [ -n "${IMAGE_DIGEST_OVERRIDE}" ];
then
    echo ${IMAGE_DIGEST_OVERRIDE}
    exit 0
fi

if [ -n "${IMAGE_DIGEST}" ];
then
    echo ${IMAGE_DIGEST}
    exit 0
fi


tags=$(mktemp)
trap "rm ${tags}" EXIT

az acr repository show-tags --orderby time_desc --n ${aro_hcp_image_acr} --repository ${repository} --detail > $tags

suggested_digest=$(jq -r --arg TAG $(git rev-parse --short=7 HEAD) \
    'first(.[] | select(.name==$TAG) | .digest)' $tags)
if [ -n "${suggested_digest}" ];
then
    echo ${suggested_digest}
    exit 0
fi

jq -r 'first(.[] | .digest)' $tags