#!/bin/bash

# deploy a service to a cluster
# ./svc-deploy <deploy-env> <dir> <cluster> [target]
# this script expects the <dir> to contain a Makefile that takes care
# of processing any config.mk template on its own

cd $(dirname "$(realpath "${BASH_SOURCE[0]}")") || exit

export DEPLOY_ENV=$1
export DIR=$2
export CLUSTER=$3
export TARGET=${4:-deploy}

if [[ "$CLUSTER" != "svc" && "$CLUSTER" != "mgmt" ]]; then
    echo "Error: CLUSTER must be either 'svc' or 'mgmt'."
    exit 1
fi

export KUBECONFIG=$(cd dev-infrastructure || exit ; make --no-print-directory $CLUSTER.aks.kubeconfigfile)

cd ${DIR} || exit
make $TARGET
