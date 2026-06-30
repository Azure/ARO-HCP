#!/bin/bash

kubectl apply -f - <<EOF
apiVersion: hcprecovery.aro-hcp.azure.com/v1alpha1
kind: HCPRecovery
metadata:
  name: 2p87p8n535epvp43meqnp1i218s6lcqg-recovery-3
  namespace: hcp-recovery
spec:
  clusterId: 2p87p8n535epvp43meqnp1i218s6lcqg
  backupId: 2p87p8n535epvp43meqnp1i218s6lcqg-hourly-20260325180003
EOF
