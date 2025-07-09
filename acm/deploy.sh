#!/bin/bash

set -e

kubectl create namespace ${MCE_NS} --dry-run=client -o json | kubectl apply -f -

set +e
# Ensure smooth upgrade from mce 2.7.0 to 2.8.1
helm uninstall --ignore-not-found \
    clc-state-metrics \
    --namespace ${MCE_NS}
set -e

phase=$(kubectl -n multicluster-engine get mce multiclusterengine -o json | jq -r '.status.phase')

if [ "${phase}" = "Paused" ]; then
    echo "MCE is already paused, skipping deploy"
    exit 0
fi
${HELM_CMD} \
    mce ${MCE_CHART_DIR} \
    --namespace ${MCE_NS} \
    --set imageRegistry=${REGISTRY}
${HELM_CMD} \
    mce-config ${MCE_CONFIG_DIR} \
    --namespace ${MCE_NS} \
    --set global.registryOverride=${REGISTRY}

if [ "${DRY_RUN}" != "true" ]; then
    kubectl annotate mce multiclusterengine installer.multicluster.openshift.io/pause=${MCE_PAUSE_RECONCILIATION} --overwrite
fi

make deploy-policies