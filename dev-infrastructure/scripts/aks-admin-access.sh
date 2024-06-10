#!/bin/sh

RESOURCEGROUP=$1
CURRENTUSER_CLIENT_ID=$(az ad signed-in-user show | jq -r '.id')
CLUSTER_ID=$(az aks list -g $RESOURCEGROUP | jq -r .[0].id)

az role assignment create --assignee $CURRENTUSER_CLIENT_ID --role "Azure Kubernetes Service Cluster Admin Role" --scope $CLUSTER_ID
echo "It might take a couple of minutes for the permissions to take effect"
