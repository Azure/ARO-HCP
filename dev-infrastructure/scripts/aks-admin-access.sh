#!/bin/sh
set -e

RESOURCEGROUP=$1
# CURRENTUSER_CLIENT_ID=$(az ad signed-in-user show -o json | jq -r '.id')
CURRENTUSER_CLIENT_ID=$(az account show -o json | jq -r '.user.name')
CLUSTER_ID=$(az aks list -g $RESOURCEGROUP -o json | jq -r .[0].id)

az role assignment create --assignee $CURRENTUSER_CLIENT_ID --role "Azure Kubernetes Service RBAC Cluster Admin" --scope $CLUSTER_ID
az role assignment create --assignee $CURRENTUSER_CLIENT_ID --role "Azure Kubernetes Service Cluster Admin Role" --scope $CLUSTER_ID
echo "It might take a couple of minutes for the permissions to take effect"
