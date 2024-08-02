#!/usr/bin/env bash

set -exv

wait_resource() {
    if [ $# = 3 ]; then
        namespace=$1
        kind=$2
        name=$3
        resource="${namespace}/${kind}/${name}"
    else
        namespace=""
        kind=$1
        name=$2
        resource="${kind}/${name}"
    fi

    total_retries=60
    retries=$total_retries

    until [[ $retries == 0 ]]; do
        if [ -n "$namespace" ]; then
            kubectl get -n "${namespace}" "${kind}" "${name}" 2>/dev/null && break
        else
            kubectl get "${kind}" "${name}" 2>/dev/null && break
        fi

        sleep 10
        retries=$((retries - 1))
    done

    if [ $retries == 0 ]; then
        echo "Resource ${resource} not found."
        exit 1
    fi
}

# install prerequisites (mce namespace and imagepullsecrets)
kubectl apply -k deploy/prerequisites

# install olm resources (mce and acm catalogsources, operatorgroup and acm subscription)
kubectl apply -k deploy/olm-resources

# wait for acm to be up and running
wait_resource open-cluster-management csv advanced-cluster-management.v2.11.0
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded csv advanced-cluster-management.v2.11.0 -n open-cluster-management --timeout=600s
kubectl wait --for=condition=Established crds multiclusterhubs.operator.open-cluster-management.io --timeout=600s
kubectl rollout status -w deployment/multiclusterhub-operator -n open-cluster-management --timeout=600s

# install multiclusterhub CR
kubectl apply -f deploy/mch/multiclusterhub.yaml

# wait for MCE and CRD to be present
wait_resource multicluster-engine csv multicluster-engine.v2.6.0
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded csv multicluster-engine.v2.6.0 -n multicluster-engine --timeout=600s

# disable hypershift operator management and metrics on hypershift-addon
# we only need it to manage ManagedCluster adoption
wait_resource multicluster-engine addondeploymentconfigs hypershift-addon-deploy-config
kubectl patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=merge -p '{"spec":{"customizedVariables":[{"name":"disableMetrics","value": "true"},{"name":"disableHOManagement","value": "true"}]}}'

# tmp - override hypershift-addon to use
kubectl apply -f deploy/mch/mce-overrides.yml -n multicluster-engine
wait_resource mce multiclusterengine
kubectl annotate mce multiclusterengine --overwrite installer.multicluster.openshift.io/image-overrides-configmap=mce-overrides

# wait for MCE setup to finalize
kubectl wait --for=condition=Established crds multiclusterengines.multicluster.openshift.io --timeout=600s
kubectl rollout status -w deployment/multicluster-engine-operator -n multicluster-engine --timeout=600s
wait_resource crds manifestworks.work.open-cluster-management.io
kubectl wait --for=condition=Established crds manifestworks.work.open-cluster-management.io --timeout=600s
wait_resource crds managedclusters.cluster.open-cluster-management.io
kubectl wait --for=condition=Established crds managedclusters.cluster.open-cluster-management.io --timeout=600s

# config MCE addons to run in hosted mode
# https://github.com/stolostron/hypershift-addon-operator/blob/2c2794d5b773dfb1e5210c804faf22747dc003be/docs/advanced/running_mce_acm_addons_hostedmode.md#configuring-the-hub-cluster
kubectl apply -f deploy/mch/addondeployconfig.yaml -n multicluster-engine
kubectl patch clustermanagementaddon work-manager --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'
kubectl patch clustermanagementaddon config-policy-controller --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'
kubectl patch clustermanagementaddon cert-policy-controller --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'

# apply klusterletconfig to enroll local cluster
kubectl apply -f deploy/mch/klusterletconfig.yaml

# wait for managedcluster to join ACM Hub
wait_resource managedcluster local-cluster
kubectl wait --for=condition=ManagedClusterJoined managedcluster local-cluster --timeout=600s
kubectl wait --for=condition=ManagedClusterConditionAvailable managedcluster local-cluster --timeout=600s
kubectl wait --for=jsonpath='{.status.conditions[?(@.type=="Applied")].status}'=True -n local-cluster manifestwork local-cluster-klusterlet --timeout=600s
kubectl wait --for=jsonpath='{.status.conditions[?(@.type=="Available")].status}'=True -n local-cluster manifestwork local-cluster-klusterlet --timeout=600s

# apply managedclusterset
kubectl apply -f deploy/mch/managedclusterset.yaml
