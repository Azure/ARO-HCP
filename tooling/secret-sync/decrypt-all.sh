#!/bin/bash

function usage {
    echo "Need to set following environment variables"
    echo "\$SECRETS semicolon seperated list of secrets to set"
    echo "\$KEYVAULT keyvault containing the sync key and receiver of decrypted secret"
    echo "Optional: \$SECRETSYNCKEY sync key, defaults to: secretSyncKey"
    echo "Optional: \$DATADIRPREFIX, path to read encrypted data from defaults to: data"
    exit 1
}

if [ -z $SECRETS ] || [ -z $KEYVAULT ]; then
    usage
fi

if [ -z $SECRETSYNCKEY ]; then
    export SECRETSYNCKEY="secretSyncKey"
fi

if [ -z $DATADIRPREFIX ]; then
    export DATADIRPREFIX="data"
fi

dir_prefix=$(dirname $0)

command=${dir_prefix}/decrypt.sh

if [[ ${DRY_RUN} == "true" ]]; then
    command="echo"
fi

while  read -d';' line
do
    ${command} ${DATADIRPREFIX}/encryptedsecrets/${line} ${KEYVAULT} ${SECRETSYNCKEY}
done <<< $SECRETS
