#!/bin/bash

if [[ $# -ne 2 ]];
then
    echo "usage"
    echo "echo content | catencrypt.sh certificate.pfx outputFile"
fi

certificate=${1}
outputFile=${2}

cat < /dev/stdin | openssl pkeyutl -encrypt -certin -inkey ${certificate} -in - -out ${outputFile}
