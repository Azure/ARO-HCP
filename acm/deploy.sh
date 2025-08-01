#!/bin/bash

set -e

kubectl create namespace ${MCE_NS} --dry-run=client -o json | kubectl apply -f -

set +e
# Ensure smooth upgrade from mce 2.7.0 to 2.8.1
helm uninstall --ignore-not-found \
    clc-state-metrics \
    --namespace ${MCE_NS}
set -e

# Check if MCE resource exists
if kubectl get mce multiclusterengine -n ${MCE_NS} >/dev/null 2>&1; then
    phase=$(kubectl -n ${MCE_NS} get mce multiclusterengine -o json | jq -r '.status.phase')

    if [ "${phase}" = "Paused" ] && [ "${MCE_PAUSE_RECONCILIATION}" = "true" ]; then
        echo "MCE is already paused, skipping deploy"
        exit 0
    fi
    
    # If MCE_PAUSE_RECONCILIATION is false and MCE exists, ensure deployments are scaled up
    if [ "${MCE_PAUSE_RECONCILIATION}" = "false" ]; then
        echo "MCE_PAUSE_RECONCILIATION is false, checking for scaled-down deployments..."
        
        # Check for deployments with 0 replicas and scale them up
        mceo_replicas=$(kubectl -n ${MCE_NS} get deployment/multicluster-engine-operator -o json | jq -r '.spec.replicas')
        
        if [ "$mceo_replicas" = 0 ]; then
            echo "Found scaled-down mce operator, scaling back up..."
            if [ "${DRY_RUN}" != "true" ]; then
                kubectl -n ${MCE_NS} scale deployment/multicluster-engine-operator --replicas=2
                kubectl wait --for=condition=available --timeout=600s deployment/multicluster-engine-operator -n ${MCE_NS}
            fi
        else
            echo "No scaled-down deployments found, all deployments are running normally"
        fi
    fi
else
    echo "MCE resource does not exist yet, proceeding with normal deployment"
fi


${HELM_CMD} \
    mce ${MCE_CHART_DIR} \
    --namespace ${MCE_NS} \
    --set imageRegistry=${REGISTRY}
${HELM_CMD} \
    --timeout 1200s \
    mce-config ${MCE_CONFIG_DIR} \
    --namespace ${MCE_NS} \
    --set global.registryOverride=${REGISTRY}

#
if [ "${DRY_RUN}" != "true" ]; then
    kubectl annotate mce multiclusterengine installer.multicluster.openshift.io/pause=${MCE_PAUSE_RECONCILIATION} --overwrite
fi
