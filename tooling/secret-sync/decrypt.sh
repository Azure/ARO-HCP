#!/bin/bash

if [[ $# -ne 4 ]];
then
    echo "usage"
    echo "decrypt.sh file outputSecret key-vault privateKeySecret"
    exit 1
fi

filename=${1}
outputSecret=${2}
keyvault=${3}
privateKeySecret=${4}

decryptedSecret=$(az keyvault key decrypt --only-show-errors --name ${privateKeySecret} \
    --algorithm RSA-OAEP --vault-name "${keyvault}" \
    --data-type base64 --value "$(cat ${filename})" | jq '.result' -r | base64 -d)

current_value=$(az keyvault secret show --only-show-errors \
    --name "${outputSecret}" \
    --vault-name "${keyvault}" 2>&1)

if [[ $(echo ${current_value} |grep -c SecretNotFound) -gt 0 ]] \
    || [[ $(echo ${current_value} | jq -r '.value' ) != ${decryptedSecret} ]];
then
    echo "Setting secret ${keyvault}/${outputSecret}"
    if [[ ${DRY_RUN} == "true" ]];
    then
        exit 0
    fi
    az keyvault secret set --only-show-errors \
        --name "${outputSecret}" \
        --vault-name "${keyvault}" \
        --value "${decryptedSecret}" 2>&1 > /dev/null
    exit $?
fi

echo "Secret ${keyvault}/${outputSecret} up to date"
