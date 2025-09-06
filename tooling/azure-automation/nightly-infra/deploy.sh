#!/bin/bash

# bash will report what is happening and stop in case of non zero return code
set -xe
echo "[INFO] Starting deploy.sh..."

if [ "$#" -lt 3 ]; then
  echo "[ERROR] Usage: $0 <RESOURCE_GROUP> <ENV_NAME> <TARGET_SUBSCRIPTION_ID>"
  exit 1
fi

RESOURCE_GROUP=$1
ENV_NAME=$2           # svc | mgnt | global
TARGET_SUBSCRIPTION_ID=$3

LOCATION="eastus"
DEPLOYMENT_NAME="deploy-${ENV_NAME}"

case "$ENV_NAME" in
  "svc")
    TEMPLATE_FILE="../../../dev-infrastructure/templates/svc-infra.bicep"
    PARAM_FILE="svc-infra.parameters.json"
    ;;
  "mgnt")
    TEMPLATE_FILE="../../../dev-infrastructure/templates/mgmt-infra.bicep"
    ;;
  "global")
    TEMPLATE_FILE="../../../dev-infrastructure/templates/global-infra.bicep"
    PARAM_FILE="../../../dev-infrastructure/configurations/global-infra.tmpl.bicepparam"
    ;;
  *)
    echo "[ERROR] Unknown ENV_NAME: $ENV_NAME. Must be one of: svc, mgnt, global"
    exit 1
    ;;
esac

echo "[INFO] Logging in with Managed Identity..."
az login --identity || { echo "[ERROR] Login failed"; exit 1; }

echo "[INFO] Switching to subscription: $TARGET_SUBSCRIPTION_ID"
az account set --subscription "$TARGET_SUBSCRIPTION_ID"

echo "[INFO] Creating resource group: $RESOURCE_GROUP in $LOCATION"
az group create --name "$RESOURCE_GROUP" --location "$LOCATION"

echo "[INFO] Deploying Bicep template..."
az deployment group create \
  --name "$DEPLOYMENT_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --template-file "$TEMPLATE_FILE" \
  --parameters @"$PARAM_FILE" \
              #  location="$LOCATION" \
              #  envName="$ENV_NAME"

echo "[SUCCESS] Deployment completed for environment: $ENV_NAME"
