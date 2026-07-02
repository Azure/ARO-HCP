#!/bin/bash
set -euo pipefail

# Creates and/or tags the Swift management VNet so that it carries
# stampcreatorserviceinfo=true. That tag only triggers Swift stamp registration when it is
# written by an identity registered for Swift usage with the network RP (globalMSI).
#
# ARM deployment scripts used to provide that identity elevation, but they auto-create a
# shared-key storage account that Azure Policy now blocks (SFI-ID4.2.1). A plain Shell step
# cannot be used directly either: templatize ignores shellIdentity, so the write would run as
# the ambient (non-Swift-registered) identity and the stamp would never register.
#
# Instead this step launches a single container group that is assigned globalMSI and performs
# the create/tag AS globalMSI. Everything (create, wait, log, cleanup) happens in this one
# Shell step so there is no cross-step ordering/async race: we start the container group
# asynchronously (az container create --no-wait) and then poll it to completion. The ambient
# identity only launches the container; the VNet write itself is performed by globalMSI from
# inside it.
#
# Inputs via environment variables:
#   ENABLE_SWIFT          - Whether Swift VNet creation/tagging should run ("true"/"false")
#   VNET_NAME             - Name of the Swift VNet to create/tag
#   VNET_ADDRESS_PREFIX   - Address space used when the VNet must be created
#   RESOURCE_GROUP        - Resource group that holds the VNet and the container group
#   SUBSCRIPTION_ID       - Subscription that holds the resource group
#   DEPLOYMENT_MSI_ID     - Resource ID of the Swift-registered managed identity (globalMSI)

if [ "${ENABLE_SWIFT:-false}" != "true" ]; then
  echo "Swift VNet is disabled; skipping."
  exit 0
fi

CONTAINER_IMAGE="mcr.microsoft.com/azure-cli:2.53.1"
CONTAINER_GROUP_NAME="swift-vnet-${VNET_NAME}"
TIMEOUT=600
POLL_INTERVAL=10

az account set --subscription "${SUBSCRIPTION_ID}"

# Remove any leftover container group from a previous (failed) attempt so the create below is
# deterministic and the name does not collide.
az container delete \
  --resource-group "${RESOURCE_GROUP}" \
  --name "${CONTAINER_GROUP_NAME}" \
  --yes >/dev/null 2>&1 || true

# Script executed inside the container, AS globalMSI. Values are baked in here (none are
# secret) and the whole script is base64-encoded below so container command-line quoting
# cannot mangle it. The managed identity's Network/Tag Contributor role assignments are
# created by the preceding swift-vnet-permissions step; Azure RBAC is eventually consistent,
# so the first write can fail with AuthorizationFailed until the assignment propagates - the
# retry helper absorbs that lag.
read -r -d '' CONTAINER_SCRIPT <<EOF || true
set -euo pipefail

echo "Logging in with the Swift-registered managed identity..."
az login --identity --username "${DEPLOYMENT_MSI_ID}" --output none
az account set --subscription "${SUBSCRIPTION_ID}"

MAX_WAIT=180
POLL_INTERVAL=5

retry() {
  local start=\${SECONDS}
  until "\$@"; do
    if [ \$((SECONDS - start)) -ge "\${MAX_WAIT}" ]; then
      echo "'\$*' still failing after \$((SECONDS - start))s" >&2
      return 1
    fi
    echo "Command failed (likely RBAC propagation); retrying in \${POLL_INTERVAL}s..." >&2
    sleep "\${POLL_INTERVAL}"
  done
}

# Wait until the managed identity can actually read the resource group before deciding whether
# the VNet exists. The Network/Tag Contributor assignments are created in a preceding step and
# Azure RBAC is eventually consistent, so without this gate a transient auth failure on the
# existence check could misroute a re-run (existing VNet) into the create path.
retry az group show --name "${RESOURCE_GROUP}" --output none

# Decide whether the VNet already exists, distinguishing a genuine NotFound (create path) from
# a transient/AuthorizationFailed error (retry the read). The az group show gate above already
# waits for RBAC to propagate, but we still never want a transient read failure to misroute an
# existing VNet into the create path - only a real NotFound should reach create.
vnet_exists=""
show_start=\${SECONDS}
while true; do
  if show_err=\$(az network vnet show --resource-group "${RESOURCE_GROUP}" --name "${VNET_NAME}" --output none 2>&1); then
    vnet_exists="yes"
    break
  fi
  if printf '%s' "\${show_err}" | grep -qiE "ResourceNotFound|was not found|could not be found"; then
    vnet_exists="no"
    break
  fi
  if [ \$((SECONDS - show_start)) -ge "\${MAX_WAIT}" ]; then
    echo "VNet existence check still failing after \$((SECONDS - show_start))s: \${show_err}" >&2
    exit 1
  fi
  echo "VNet show failed (non-NotFound, likely transient/RBAC propagation); retrying in \${POLL_INTERVAL}s..." >&2
  sleep "\${POLL_INTERVAL}"
done

if [ "\${vnet_exists}" = "yes" ]; then
  echo "VNet ${VNET_NAME} exists. Updating Swift tag..."
  retry az resource tag \
    --is-incremental \
    --tags stampcreatorserviceinfo=true \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${VNET_NAME}" \
    --resource-type Microsoft.Network/virtualNetworks \
    --api-version 2024-05-01
else
  echo "VNet ${VNET_NAME} does not exist. Creating..."
  retry az network vnet create \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${VNET_NAME}" \
    --address-prefixes "${VNET_ADDRESS_PREFIX}" \
    --tags stampcreatorserviceinfo=true
fi

echo "Swift VNet ${VNET_NAME} is ready and tagged."
EOF

CONTAINER_SCRIPT_B64=$(printf '%s' "${CONTAINER_SCRIPT}" | base64 | tr -d '\n')

echo "Launching container group ${CONTAINER_GROUP_NAME} as globalMSI..."
az container create \
  --resource-group "${RESOURCE_GROUP}" \
  --name "${CONTAINER_GROUP_NAME}" \
  --image "${CONTAINER_IMAGE}" \
  --os-type Linux \
  --restart-policy Never \
  --cpu 1 \
  --memory 1.5 \
  --assign-identity "${DEPLOYMENT_MSI_ID}" \
  --command-line "/bin/bash -c 'echo ${CONTAINER_SCRIPT_B64} | base64 -d | bash'" \
  --no-wait

container_state() {
  az container show \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CONTAINER_GROUP_NAME}" \
    --query "containers[0].instanceView.currentState.state" \
    --output tsv 2>/dev/null || echo ""
}

group_provisioning_state() {
  az container show \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CONTAINER_GROUP_NAME}" \
    --query "provisioningState" \
    --output tsv 2>/dev/null || echo ""
}

start=${SECONDS}
while true; do
  state=$(container_state)
  if [ "${state}" = "Terminated" ]; then
    break
  fi
  # Fail fast if the container group itself fails to provision: the container may never
  # reach a Terminated state, so without this check we would wait the full TIMEOUT.
  if [ "$(group_provisioning_state)" = "Failed" ]; then
    echo "✗ Container group ${CONTAINER_GROUP_NAME} failed to provision" >&2
    az container show --resource-group "${RESOURCE_GROUP}" --name "${CONTAINER_GROUP_NAME}" \
      --query "containers[0].instanceView.events" --output json 2>/dev/null || true
    az container logs --resource-group "${RESOURCE_GROUP}" --name "${CONTAINER_GROUP_NAME}" || true
    az container delete --resource-group "${RESOURCE_GROUP}" --name "${CONTAINER_GROUP_NAME}" --yes >/dev/null 2>&1 || true
    exit 1
  fi
  if [ $((SECONDS - start)) -ge "${TIMEOUT}" ]; then
    echo "✗ Timed out after $((SECONDS - start))s waiting for ${CONTAINER_GROUP_NAME} (last state: ${state:-unknown})" >&2
    az container logs --resource-group "${RESOURCE_GROUP}" --name "${CONTAINER_GROUP_NAME}" || true
    az container delete --resource-group "${RESOURCE_GROUP}" --name "${CONTAINER_GROUP_NAME}" --yes >/dev/null 2>&1 || true
    exit 1
  fi
  echo "Waiting for ${CONTAINER_GROUP_NAME} to finish (state: ${state:-pending})..."
  sleep "${POLL_INTERVAL}"
done

echo "=== ${CONTAINER_GROUP_NAME} logs ==="
az container logs --resource-group "${RESOURCE_GROUP}" --name "${CONTAINER_GROUP_NAME}" || true

exit_code=$(az container show \
  --resource-group "${RESOURCE_GROUP}" \
  --name "${CONTAINER_GROUP_NAME}" \
  --query "containers[0].instanceView.currentState.exitCode" \
  --output tsv 2>/dev/null || echo "")

echo "Cleaning up ${CONTAINER_GROUP_NAME}..."
az container delete \
  --resource-group "${RESOURCE_GROUP}" \
  --name "${CONTAINER_GROUP_NAME}" \
  --yes >/dev/null 2>&1 || true

if [ "${exit_code}" != "0" ]; then
  echo "✗ ${CONTAINER_GROUP_NAME} exited with code ${exit_code:-unknown}" >&2
  exit 1
fi

echo "Swift VNet ${VNET_NAME} created/tagged successfully."
