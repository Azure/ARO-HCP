#!/bin/bash

if [ "$#" -ne 3 ]
then
    echo "Need Registry, Repository and Tag parameters"
    exit 1
fi

registry=${1}
repository=${2}
tag=${3}

az acr repository show-tags \
    -n ${registry} \
    --repository ${repository} \
    --detail --orderby time_desc | jq --arg TAG "${tag}" '.[] | select(.name==$TAG)|.digest' -r
