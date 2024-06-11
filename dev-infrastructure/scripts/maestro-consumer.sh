#!/bin/bash

oc process --local -f https://raw.githubusercontent.com/openshift-online/maestro/main/templates/agent-template-aro-hcp.yml \
    IMAGE_REGISTRY=quay.io \
    IMAGE_REPOSITORY=redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro \
    IMAGE_TAG=ae149df618cb0812d2072b20658a9cff84c087eb \
    AGENT_SA=maestro \
    AGENT_NAMESPACE=maestro | oc apply -f - -n maestro

CLUSTER_NAME=`kubectl config view -o=jsonpath='{.clusters[0].name}'`

cat << EOF | curl -X POST -H "Content-Type: application/json" -d @- http://localhost:8000/api/maestro/v1/consumers
{
  "name": "$CLUSTER_NAME"
}
EOF
