#!/bin/bash

oc process --local -f https://raw.githubusercontent.com/openshift-online/maestro/main/templates/agent-template-aro-hcp.yml \
    IMAGE_REGISTRY=quay.io \
    IMAGE_REPOSITORY=redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro \
    IMAGE_TAG=8ad292b980c2c8aa8a1a73dd698859f6e324f5a1 \
    AGENT_SA=maestro \
    AGENT_NAMESPACE=maestro | oc apply -f - -n maestro

CLUSTER_NAME=`kubectl config view -o=jsonpath='{.clusters[0].name}'`

cat << EOF | curl -X POST -H "Content-Type: application/json" -d @- http://localhost:8000/api/maestro/v1/consumers
{
  "name": "$CLUSTER_NAME"
}
EOF
