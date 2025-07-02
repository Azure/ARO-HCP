#!/bin/bash
set -euo pipefail

# Deploy Azure Container App infrastructure for nightly jobs
# Usage: ./deploy-infra.sh <resource-group> <location> <acr-name> <environment>
RESOURCE_GROUP=${1:-"rg-aro-hcp-ntly"}
LOCATION=${2:-"uksouth"}
ACR_NAME=${3:-"arohcpsvcdev"}
ENVIRONMENT=${4:-"ntly"}

# Resource names
ENV_NAME="aro-hcp-${ENVIRONMENT}-env"
JOB_NAME="aro-hcp-${ENVIRONMENT}-job"
LOGS_NAME="aro-hcp-${ENVIRONMENT}-logs"
IMAGE_NAME="${ACR_NAME}.azurecr.io/ntly-infra:latest"

echo "Deploying to: $RESOURCE_GROUP ($LOCATION)"

# Check Azure CLI login
if ! az account show &>/dev/null; then
    echo "Error: Not logged in to Azure. Run 'az login' first."
    exit 1
fi

# Create resource group if needed
if ! az group show --name "$RESOURCE_GROUP" &>/dev/null; then
    echo "Creating resource group..."
    az group create --name "$RESOURCE_GROUP" --location "$LOCATION" --output none
fi

# Create Log Analytics workspace
echo "Creating Log Analytics workspace..."
az monitor log-analytics workspace create \
    --resource-group "$RESOURCE_GROUP" \
    --workspace-name "$LOGS_NAME" \
    --location "$LOCATION" \
    --retention-time 30 \
    --sku PerGB2018 \
    --output none 2>/dev/null || true

# Get workspace credentials
WORKSPACE_ID=$(az monitor log-analytics workspace show \
    --resource-group "$RESOURCE_GROUP" \
    --workspace-name "$LOGS_NAME" \
    --query customerId \
    --output tsv)

WORKSPACE_KEY=$(az monitor log-analytics workspace get-shared-keys \
    --resource-group "$RESOURCE_GROUP" \
    --workspace-name "$LOGS_NAME" \
    --query primarySharedKey \
    --output tsv)

# Create Container App environment
echo "Creating Container App environment..."
az containerapp env create \
    --name "$ENV_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --location "$LOCATION" \
    --logs-workspace-id "$WORKSPACE_ID" \
    --logs-workspace-key "$WORKSPACE_KEY" \
    --output none 2>/dev/null || true

# Create Container App Job
echo "Creating Container App Job..."
az containerapp job create \
    --name "$JOB_NAME" \
    --resource-group "$RESOURCE_GROUP" \
    --environment "$ENV_NAME" \
    --trigger-type "Schedule" \
    --cron-expression "0 2 * * *" \
    --replica-timeout 1800 \
    --replica-retry-limit 3 \
    --replica-completion-count 1 \
    --parallelism 1 \
    --image "$IMAGE_NAME" \
    --cpu "0.5" \
    --memory "1Gi" \
    --env-vars "DEPLOY_ENV=$ENVIRONMENT" \
    --registry-server "${ACR_NAME}.azurecr.io" \
    --registry-identity "system" \
    --output none

echo "Deployment complete! Monitor: az containerapp job execution list --name $JOB_NAME --resource-group $RESOURCE_GROUP"