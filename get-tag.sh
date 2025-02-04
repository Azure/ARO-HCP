#!/bin/bash
set -x 
if [ "$#" -ne 2 ]
then
    echo "Need ARO_HCP_IMAGE_ACR and REPOSITORY parameters"
    exit 1
fi

aro_hcp_image_acr=${1}
repository=${2}

if [ ! -z "${COMMIT_OVERRIDE}" ];
then
    echo ${COMMIT_OVERRIDE}
    exit 0
fi

if [ ! -z "${COMMIT}" ];
then
    echo ${COMMIT}
    exit 0
fi


tags=$(mktemp)
trap "rm ${tags}" EXIT

az acr repository show-tags --orderby time_desc --n ${aro_hcp_image_acr} --repository ${repository} > $tags

suggested_tag=$(grep $(git rev-parse --short=7 HEAD) $tags |cut -d '"' -f2)
if [ ! -z "${suggested_tag}" ];
then
    echo ${suggested_tag}
    exit 0
fi

grep '"' $tags | head -1 | cut -d '"' -f2
