#!/bin/bash

set -eu

if [[ $# -ne 3 ]];
then
    echo "usage"
    echo "decrypt.sh file key-vault privateKeySecret"
    echo ""
    echo "Use - as file parameter for stdin"
    echo "cat secret.out | decrypt.sh - key-vault privateKeySecret"
    exit 1
fi

filename=${1}
keyvault=${2}
privateKeySecret=${3}

outputSecret=$(basename -s '.enc' ${filename})

decryptedSecret=$(az keyvault key decrypt --name ${privateKeySecret} \
    --algorithm RSA-OAEP --vault-name "${keyvault}" \
    --data-type base64 --value "$(cat ${filename})" | jq '.result' -r | base64 -d)

az keyvault secret set \
    --name "${outputSecret}" \
    --vault-name "${keyvault}" \
    --value "${decryptedSecret}"
