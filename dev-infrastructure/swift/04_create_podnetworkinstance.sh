#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars

if [ $# -lt 1 ]; then
  echo "$0 takes a single namepace name for an argument"
  exit 1
elif [ $# -eq 1 ]; then
  NAMESPACE=$1
fi

if ! is_redhat_user; then
    az login
fi

kubectl apply -f - << EOF 
apiVersion: multitenancy.acn.azure.com/v1alpha1
kind: PodNetworkInstance
metadata:
  finalizers:
  - finalizers.acn.azure.com/dnc-operations
  name: pni1
  namespace: $NAMESPACE
spec:
  podNetworkConfigs:
    - podNetwork: pn1
      podIPReservationSize: 3
EOF
