#!/bin/bash

outputFile=${1}

secret=$(cat < /dev/stdin)

dir_prefix=$(dirname $0)

cd ${dir_prefix}
make secret-sync
cd -

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

    echo ${secret} | \
    PUBLIC_KEY_FILE=${DATADIRPREFIX}/keys/${keyfile} \
    OUTPUT_FILE=${targetFile} \
    ${dir_prefix}/secret-sync encrypt
done
