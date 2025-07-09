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
        scaled_down_deployments=$(kubectl -n ${MCE_NS} get deployments -o json | jq -r '.items[] | select(.spec.replicas == 0) | .metadata.name')
        
        if [ -n "$scaled_down_deployments" ]; then
            echo "Found scaled-down deployments, scaling them back up..."
            for deployment in $scaled_down_deployments; do
                echo "Scaling up deployment: $deployment"
                if [ "${DRY_RUN}" != "true" ]; then
                    kubectl -n ${MCE_NS} scale deployment/$deployment --replicas=2
                fi
            done
            echo "All deployments scaled back up to 2 replicas"
        else
            echo "No scaled-down deployments found, all deployments are running normally"
        fi
    fi
else
    echo "MCE resource does not exist yet, proceeding with normal deployment"
fi

if [ "${DRY_RUN}" != "true" ]; then
    echo "Waiting for multicluster-engine-operator deployment to be available..."
    kubectl wait --for=condition=available --timeout=600s deployment/multicluster-engine-operator -n ${MCE_NS}
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

make deploy-policies