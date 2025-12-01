#!/bin/bash

set -euo pipefail

KV_NAME=$1
shift
SECRET_NAME=$1
shift
CRT_BUNDLE=$1
shift

TMP_DIR=$(mktemp -d)

cleanup() {
  echo "Cleanup cert bundle ${TMP_DIR} ..."
	rm -rf "${TMP_DIR}"
}
trap cleanup EXIT INT TERM

PFX="${TMP_DIR}/cert.pfx"

az keyvault secret download --vault-name "${KV_NAME}" --name "${SECRET_NAME}" --file "${PFX}" --encoding base64

openssl pkcs12 -in "${PFX}" -out "${TMP_DIR}/bundle.crt" -nodes -passin pass:
ln -s "${TMP_DIR}/bundle.crt" "${CRT_BUNDLE}"

"$@"
