#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars

if ! is_redhat_user; then
    az login
fi

kubectl apply -f - << EOF 
multitenancy.acn.azure.com/v1alpha1
kind: PodNetworkInstance
metadata:
  finalizers:
  - finalizers.acn.azure.com/dnc-operations
  name: pni1
  namespace: default
spec:
  podNetworkconfigs:
    - podnetwork: pn1
      podIPReservationSize: 3
EOF
