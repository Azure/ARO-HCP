#!/bin/bash

set -eu

if [[ $# -ne 2 ]];
then
    echo "usage"
    echo "echo content | encrypt.sh rsa-public.pem outputFile"
fi

keyfile=${1}
outputFile=${2}

cat < /dev/stdin | openssl pkeyutl -encrypt \
    -pkeyopt rsa_padding_mode:oaep \
    -pubin -inkey ${keyfile}  -in - | base64 -w0 > ${outputFile}
