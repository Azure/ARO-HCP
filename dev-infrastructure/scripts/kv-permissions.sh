#!/bin/bash

PRINCIPAL_ID=$1
RG_NAME=$2
KV_NAME=$3

KV_RESOURCE_ID=$(az keyvault show --name ${KV_NAME} --resource-group ${RG_NAME} --query id -o tsv 2>/dev/null)

if [ -z "${KV_RESOURCE_ID}" ]; then
    echo "Error: Key Vault resource ID for ${KV_NAME} in ${RG_NAME} could not be retrieved."
    exit 0
fi

az role assignment create \
    --role "Key Vault Secrets Officer" \
    --assignee ${PRINCIPAL_ID} \
    --scope ${KV_RESOURCE_ID} \
    --only-show-errors

az role assignment create \
    --role "Key Vault Certificates Officer" \
    --assignee ${PRINCIPAL_ID} \
    --scope ${KV_RESOURCE_ID} \
    --only-show-errors

az role assignment create \
    --role "Key Vault Certificate User" \
    --assignee ${PRINCIPAL_ID} \
    --scope ${KV_RESOURCE_ID} \
    --only-show-errors
