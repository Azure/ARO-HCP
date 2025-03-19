#!/bin/bash

function usage {
    echo "Need to set following environment variables"
    echo "\$SECRETFOLDER Folder containing secrets to sync to the configured keyvault"
    echo "\$KEYVAULT keyvault containing the sync key and receiver of decrypted secret"
    echo "Optional: \$SECRETSYNCKEY sync key, defaults to: secretSyncKey"
    echo "Optional: \$DATADIRPREFIX, path to read encrypted data from defaults to: data"
    exit 1
}

if [ -z ${SECRETFOLDER} ] || [ -z ${KEYVAULT} ]; then
    usage
fi

if [[ ${SECRETFOLDER} == "none" ]]; then
    exit 0
fi

if [ -z ${SECRETSYNCKEY} ]; then
    export SECRETSYNCKEY="secretSyncKey"
fi

if [ -z ${DATADIRPREFIX} ]; then
    export DATADIRPREFIX="data"
fi

dir_prefix=$(dirname $0)

cd ${dir_prefix}
make secret-sync
cd -

ls -1 ${DATADIRPREFIX}/encryptedsecrets/${SECRETFOLDER} | while  read fileName
do
    SECRET_TO_SET=$(basename -s .enc ${fileName}) \
    ENCRYPTION_KEY=${SECRETSYNCKEY} \
    INPUT_FILE=${DATADIRPREFIX}/encryptedsecrets/${SECRETFOLDER}/${fileName} \
    ${dir_prefix}/secret-sync decrypt
done
