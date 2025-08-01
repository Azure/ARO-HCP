#!/bin/bash -x

set -o errexit
set -o nounset
set -o pipefail

source sal_env_vars

PFX=fpa.pfx
CRT=fpa.crt

if [ -f $PFX ]; then
  rm $PFX
fi

if [ -f $CRT ]; then
  rm $CRT
fi

if is_service_principal; then
  echo "Already logged in as SP"
  az account show --query user.name --output tsv
  exit 0
fi

echo "Get secret from KV"
az keyvault secret download \
  --vault-name aro-hcp-dev-svc-kv \
  --name firstPartyCert2 \
  --file $PFX \
  --encoding base64

echo "Creating cert"
openssl pkcs12 -in $PFX -out $CRT -nodes

AZURE_TENANT_ID="64dc69e4-d083-49fc-9569-ebece1dd1408"
AZURE_SUBSCRIPTION_ID="1d358ef8d-8973-4378-9ba4-3f9027df171b"
AZURE_CLIENT_ID="b3cb2fab-15cb-4583-ad06-f91da9bfe2d1"

echo "Logging in as SP:download"
az login --service-principal --username $AZURE_CLIENT_ID --certificate $CRT --tenant $AZURE_TENANT_ID

