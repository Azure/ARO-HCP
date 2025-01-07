#!/bin/sh
set -e

RESOURCEGROUP=$1
AKS_NAME=$2
FILENAME=$3

az aks get-credentials --overwrite-existing --only-show-errors -n ${AKS_NAME} -g ${RESOURCEGROUP} -f ${FILENAME}
kubelogin convert-kubeconfig -l azurecli --kubeconfig "${FILENAME}"
