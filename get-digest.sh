#!/bin/bash

if [ "$#" -ne 3 ]
then
    echo "Usage: $0 <ARO_HCP_IMAGE_ACR> <REPOSITORY> <TAG>"
    exit 1
fi

aro_hcp_image_acr=${1}
repository=${2}
tag=${3}

tags=$(az acr repository show-tags --orderby time_desc --n "${aro_hcp_image_acr}" --repository "${repository}" --detail)

# find the digest for the specified tag
suggested_digest=$(jq -r --arg TAG "${tag}" \
    'first(.[] | select(.name==$TAG) | .digest)' <<< "${tags}")
if [ -n "${suggested_digest}" ] && [ "${suggested_digest}" != "null" ];
then
    echo "${suggested_digest}"
    exit 0
fi

# if there is no digest for the specified tag, return the first digest from the list of non-dirty tags
jq -r 'first(.[] | select(.name | endswith("-dirty") | not) | .digest)' <<< "${tags}"
