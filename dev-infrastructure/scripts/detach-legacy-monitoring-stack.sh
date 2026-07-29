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
# INT/STG/PROD use EV2 which names stacks deterministically as
# "ServiceGroup.LogicalRG.StepName" without tags. Dev uses templatize
# which sets serviceGroup/stepName tags. We try both lookup methods.
#
# This is a no-op once the legacy stack no longer exists.

detach_stack() {
  local stack_name="$1"
  echo "Detaching legacy stack: ${stack_name}"
  az stack group delete \
    --resource-group "${KUSTO_RESOURCE_GROUP}" \
    --name "${stack_name}" \
    --action-on-unmanage detachAll \
    --yes
  echo "Legacy stack ${stack_name} detached successfully."
}

found=false

# EV2 environments: deterministic stack name
EV2_LEGACY_STACK="Monitoring.kusto.kusto-monitoring"
if az stack group show \
  --resource-group "${KUSTO_RESOURCE_GROUP}" \
  --name "${EV2_LEGACY_STACK}" >/dev/null 2>&1; then
  detach_stack "${EV2_LEGACY_STACK}"
  found=true
fi

# Dev environments: tag-based lookup (templatize sets these tags)
if [[ "${found}" == "false" ]]; then
  legacy=$(az stack group list \
    --resource-group "${KUSTO_RESOURCE_GROUP}" \
    --query "[?tags.serviceGroup=='Microsoft.Azure.ARO.HCP.Monitoring' && tags.stepName=='kusto-monitoring'].name" \
    -o tsv)

  for stack_name in ${legacy}; do
    detach_stack "${stack_name}"
    found=true
  done
fi

if [[ "${found}" == "false" ]]; then
  echo "No legacy monitoring-scope kusto-monitoring stack found — nothing to do."
fi
