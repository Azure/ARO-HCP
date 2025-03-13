#!/bin/bash

set -eu

if [[ $# -ne 3 ]];
then
    echo "usage"
    echo "decrypt.sh file outputSecret key-vault privateKeySecret"
    exit 1
fi

filename=${1}
outputSecret=${2}
keyvault=${3}
privateKeySecret=${4}

decryptedSecret=$(az keyvault key decrypt --name ${privateKeySecret} \
    --algorithm RSA-OAEP --vault-name "${keyvault}" \
    --data-type base64 --value "$(cat ${filename})" | jq '.result' -r | base64 -d)

az keyvault secret set \
    --name "${outputSecret}" \
    --vault-name "${keyvault}" \
    --value "${decryptedSecret}"
