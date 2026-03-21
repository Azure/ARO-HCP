#!/bin/bash

kubectl apply -f - <<EOF
apiVersion: hcprecovery.aro-hcp.azure.com/v1alpha1
kind: HCPRecovery
metadata:
  name: 2p4uvputf8eu54hcl00qf9a4jfvp28ui-recovery
  namespace: hcp-recovery
spec:
  clusterId: 2p4qv2kv0sl9mtd7700ap37d9eaorub5
  backupId: 2p4qv2kv0sl9mtd7700ap37d9eaorub5-hourly-20260320230026
EOF
