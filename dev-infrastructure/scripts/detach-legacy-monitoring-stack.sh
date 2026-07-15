#!/bin/bash
set -euo pipefail

# Detach the legacy Monitoring-scope kusto-monitoring deployment stack
# so the new Geography-scope stack can adopt its resources.
#
# The legacy stack was created by monitoring-pipeline.yaml under service
# group Microsoft.Azure.ARO.HCP.Monitoring. After moving kusto-monitoring
# to geography-pipeline.yaml, the new stack has a different identity but
# targets the same resources. Azure only allows one stack to manage a
# resource, so the legacy stack must be detached first.
#
# This is a no-op once the legacy stack no longer exists.

legacy=$(az stack group list \
  --resource-group "${KUSTO_RESOURCE_GROUP}" \
  --query "[?tags.serviceGroup=='Microsoft.Azure.ARO.HCP.Monitoring' && tags.stepName=='kusto-monitoring'].name" \
  -o tsv)

if [[ -z "${legacy}" ]]; then
  echo "No legacy monitoring-scope kusto-monitoring stack found — nothing to do."
  exit 0
fi

for stack_name in ${legacy}; do
  echo "Detaching legacy stack: ${stack_name}"
  az stack group delete \
    --resource-group "${KUSTO_RESOURCE_GROUP}" \
    --name "${stack_name}" \
    --action-on-unmanage detachAll \
    --yes
  echo "Legacy stack ${stack_name} detached successfully."
done
