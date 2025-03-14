#!/bin/bash

if [[ $# -gt 2 ]] || [[ $# -ne 1 ]];
then
    echo "usage:"
    echo "To encrypt only a single secret with a single output, run:"
    echo "echo content | encrypt.sh rsa-public.pem outputFile"
    echo ""
    echo "To encrypt a secret a secret with all available keys, run:"
    echo "echo content | encrypt.sh outputFile"
    echo "Optional: \$DATADIRPREFIX, path to read/store data from/to defaults to: dev-infrastructure/data"
    echo ""
    exit 1
fi

if [[ $# -eq 2 ]];
then
    keyfile=${1}
    outputFile=${2}
    cat < /dev/stdin | openssl pkeyutl -encrypt \
    -pkeyopt rsa_padding_mode:oaep \
    -pubin -inkey ${keyfile}  -in - | base64 -w0 > ${outputFile}
fi

if [[ $# -eq 1 ]];
then
    outputFile=${1}

    secret=$(cat < /dev/stdin)

    if [ -z ${DATADIRPREFIX} ]; then
        export DATADIRPREFIX="dev-infrastructure/data"
    fi
    ls -1 "${DATADIRPREFIX}/keys" |grep pem | while read keyfile;
    do
        deployEnv=$(echo $keyfile | cut -d '_' -f1)
        keyVault=$(echo $keyfile | cut -d '_' -f2)


        targetFolder=${DATADIRPREFIX}/encryptedsecrets/${deployEnv}/${keyVault}/
        [[ ! -d ${targetFolder} ]] && mkdir -p ${targetFolder}
        targetFile=${DATADIRPREFIX}/encryptedsecrets/${deployEnv}/${keyVault}/${outputFile}

        echo ${secret} | openssl pkeyutl -encrypt \
            -pkeyopt rsa_padding_mode:oaep \
            -pubin -inkey ${DATADIRPREFIX}/keys/${keyfile}  -in - | base64 -w0 > ${targetFile}
    done
fi

