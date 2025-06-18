#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

rg=${1}

nsp=$(az network perimeter list -g ${rg} --query '[].id' --output tsv)

if [[ -n ${nsp} ]];
then
    az network perimeter delete --yes --force --ids ${nsp}
fi
