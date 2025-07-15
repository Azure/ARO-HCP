#!/bin/bash
set -euo pipefail

STORAGE_ACCOUNT="$1"
RESOURCEGROUP="$2"
AFD_ENDPOINT="$3"

PUBLIC_ACCESS_ENABLED=$(az storage account show -n "${STORAGE_ACCOUNT}" -g "${RESOURCEGROUP}" --query allowBlobPublicAccess -o tsv)
if [ "$PUBLIC_ACCESS_ENABLED" == "true" ]; then
    BLOB_ENDPOINT=$(az storage account show -n "${STORAGE_ACCOUNT}" -g "${RESOURCEGROUP}" --query primaryEndpoints.web -o tsv)
    echo "$BLOB_ENDPOINT"
else
    echo "$AFD_ENDPOINT"
fi
