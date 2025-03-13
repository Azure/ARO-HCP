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

ls -1 ${DATADIRPREFIX}/encryptedsecrets/${SECRETFOLDER} | while  read fileName
do
    secretName=$(basename -s .enc ${fileName})
    ${dir_prefix}/decrypt.sh ${DATADIRPREFIX}/encryptedsecrets/${SECRETFOLDER}/${fileName} ${secretName} ${KEYVAULT} ${SECRETSYNCKEY}
done
