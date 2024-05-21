#!/bin/bash

oc process --local -f https://raw.githubusercontent.com/openshift-online/maestro/main/templates/agent-template-aro-hcp.yml \
    IMAGE_REGISTRY=quay.io \
    IMAGE_REPOSITORY=redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro \
    IMAGE_TAG=0ba050b00ef480cf2da6bd937a0ca3711c51644d \
    AGENT_SA=maestro \
    AGENT_NAMESPACE=maestro | oc apply -f - -n maestro

CLUSTER_NAME=`kubectl config view -o=jsonpath='{.clusters[0].name}'`

cat << EOF | curl -X POST -H "Content-Type: application/json" -d @- http://localhost:8000/api/maestro/v1/consumers
{
  "name": "$CLUSTER_NAME"
}
EOF
