#!/bin/sh

CLUSTER_NAME=`kubectl config view -o=jsonpath='{.clusters[0].name}'`
MANIFEST=$(cat)
cat << EOF | curl -X POST -H "Content-Type: application/json" -d @- http://localhost:8000/api/maestro/v1/resources
{
  "consumer_name": "${CLUSTER_NAME}",
  "manifest": $MANIFEST
}
EOF
