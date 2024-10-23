#!/bin/bash

RESOURCEGROUP_NAME=$1
DEPLOYMENT_NAME=$2

az deployment group wait --name "${DEPLOYMENT_NAME}" --resource-group "${RESOURCEGROUP_NAME}" --created --updated --deleted --interval 10 || exit 0
