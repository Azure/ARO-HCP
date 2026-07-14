#!/bin/bash

kubectl apply -f - <<EOF
apiVersion: hcprecovery.aro-hcp.azure.com/v1alpha1
kind: HCPRecovery
metadata:
  name: 2rhh9f6dfgebotch8jg0862245rreeel-2
  namespace: hcp-recovery
spec:
  clusterId: 2rhh9f6dfgebotch8jg0862245rreeel
  backupId: 2rhh9f6dfgebotch8jg0862245rreeel-20260713224338 
EOF
