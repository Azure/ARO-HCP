#!/bin/bash

set -xe
echo "[INFO] Starting delete.sh..."

if [ "$#" -lt 2 ]; then
  echo "[ERROR] Usage: $0 <RESOURCE_GROUP> <TARGET_SUBSCRIPTION_ID>"
  exit 1
fi

RESOURCE_GROUP=$1
TARGET_SUBSCRIPTION_ID=$2

echo "[INFO] Logging in with Managed Identity..."
az login --identity || { echo "[ERROR] Login failed"; exit 1; }

echo "[INFO] Switching to subscription: $TARGET_SUBSCRIPTION_ID"
az account set --subscription "$TARGET_SUBSCRIPTION_ID"

RESOURCE_GROUP="rg-${ENV_NAME}"

if az group show --name "$RESOURCE_GROUP" &>/dev/null; then
    echo "[INFO] Deleting resource group: $RESOURCE_GROUP"
    az group delete --name "$RESOURCE_GROUP" --yes --no-wait
    echo "[SUCCESS] Deletion triggered."
else
    echo "[INFO] Resource group $RESOURCE_GROUP does not exist. Skipping deletion."
fi
