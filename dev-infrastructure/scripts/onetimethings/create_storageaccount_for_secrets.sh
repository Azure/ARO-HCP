#!/bin/bash

set -e

RESOURCEGROUP="global"
LOCATION="eastus"
CONTAINERNAME="secrets"
STORAGEACCOUNTNAME="hcpsharedsecretsdev"
HCPDEVSUBSCRIPTION="ARO Hosted Control Planes (EA Subscription 1)"
HCPDEVSUBSCRIPTIONID="1d3378d3-5a3f-4712-85a1-2485495dfc4b"
AROHCPENGINEERSGROUPID="366b619c-e72e-4278-8aaf-9af7851c601f" 
# id taken from 'az ad group show --group aro-hcp-engineering -o json'

az account set --subscription "$HCPDEVSUBSCRIPTION"

az group create \
  		--name $RESOURCEGROUP  \
  		--location $LOCATION \
        --tag persist=true

az storage account create \
  --name $STORAGEACCOUNTNAME \
  --resource-group $RESOURCEGROUP \
  --location $LOCATION \
  --sku Standard_LRS \
  --kind StorageV2 \
  --allow-blob-public-access false

az storage container create \
    --name $CONTAINERNAME \
    --account-name $STORAGEACCOUNTNAME \
    --auth-mode login


az storage account blob-service-properties update \
    --resource-group $RESOURCEGROUP \
    --account-name $STORAGEACCOUNTNAME \
    --enable-versioning true \
    --enable-delete-retention true \
    --delete-retention-days 7 \
    --enable-container-delete-retention true \
    --container-delete-retention-days 7 

az role assignment create \
    --role "Storage Blob Data Contributor" \
    --assignee  $AROHCPENGINEERSGROUPID \
    --scope "/subscriptions/$HCPDEVSUBSCRIPTIONID/resourceGroups/$RESOURCEGROUP/providers/Microsoft.Storage/storageAccounts/$STORAGEACCOUNTNAME"
