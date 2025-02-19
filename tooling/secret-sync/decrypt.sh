#!/bin/bash

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

secretFile=$(mktemp)

az keyvault secret show --vault-name "${keyvault}" --name "${privateKeySecret}" |jq '.value' -r |base64 -d > ${secretFile}

outputSecret=$(basename -s '.enc' ${filename})

az keyvault secret set \
    --name "${outputSecret}" \
    --vault-name "${keyvault}" \
    --value $(openssl pkeyutl -decrypt -inkey ${secretFile} -in ${filename})