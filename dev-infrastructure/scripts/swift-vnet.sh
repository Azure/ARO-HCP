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
#   VNET_NAME             - Name of the Swift VNet to create/tag
#   VNET_ADDRESS_PREFIX   - Address space used when the VNet must be created
#   RESOURCE_GROUP        - Resource group that holds the VNet and the container group
#   SUBSCRIPTION_ID       - Subscription that holds the resource group
#   DEPLOYMENT_MSI_ID     - Resource ID of the Swift-registered managed identity (globalMSI)

CONTAINER_IMAGE="mcr.microsoft.com/azure-cli:2.53.1"
CONTAINER_GROUP_NAME="swift-vnet-${VNET_NAME}"
TIMEOUT=900
POLL_INTERVAL=10
HEARTBEAT_INTERVAL=60

az account set --subscription "${SUBSCRIPTION_ID}"

# Remove any leftover container group from a previous (failed) attempt so the create below is
# deterministic and the name does not collide.
az container delete \
  --resource-group "${RESOURCE_GROUP}" \
  --name "${CONTAINER_GROUP_NAME}" \
  --yes >/dev/null 2>&1 || true

# Script executed inside the container, AS globalMSI. Values are baked in here (none are
# secret) and the whole script is base64-encoded below so container command-line quoting
# cannot mangle it.
#
# Two independent transient failure modes are handled with a bounded, fixed-interval retry
# (see retry()). Both are "wait until ready" conditions, so we poll at a short fixed interval
# to detect recovery promptly rather than backing off:
#   1. Container DNS cold-start: a freshly-created ACI in a delegated VNet subnet can come up
#      before its resolver is ready, so name resolution fails with [Errno -3] Try again
#      (EAI_AGAIN) for the first minutes. A DNS readiness gate waits on the real dependency
#      (resolving the Azure control-plane endpoints) before running any az command.
#   2. RBAC propagation: the managed identity's Network/Tag Contributor role assignments are
#      created by the preceding swift-vnet-permissions step and Azure RBAC is eventually
#      consistent, so the first reads/writes can fail with AuthorizationFailed until the
#      assignment propagates.
read -r -d '' CONTAINER_SCRIPT <<EOF || true
set -euo pipefail

# Per-stage readiness budget for retry() below (DNS gate, az login, vnet check),
# not a whole-container limit. Sized to absorb the one-off ACI network cold-start
# (resolver / RBAC readiness), which can run several minutes on the first stage
# but is warm thereafter. Total container runtime is bounded independently by the
# outer container-group TIMEOUT, which terminates the ACI regardless of how the
# per-stage budgets add up.
MAX_WAIT=480
POLL_INTERVAL=5

# retry <description> <command...>: run the command, polling at a fixed POLL_INTERVAL until it
# succeeds or MAX_WAIT is exceeded. Each attempt is logged with elapsed time so the container
# log makes clear what is being waited on and for how long. A fixed short interval (not a
# backoff) is deliberate: these are readiness waits (DNS/RBAC), so we want to catch recovery
# promptly, and a single serial caller polling its own resource poses no throttling risk.
retry() {
  local desc=\$1; shift
  local start=\${SECONDS} attempt=1 elapsed
  until "\$@"; do
    elapsed=\$((SECONDS - start))
    if [ "\${elapsed}" -ge "\${MAX_WAIT}" ]; then
      echo "[swift-vnet] \${desc}: giving up after \${attempt} attempt(s) / \${elapsed}s (limit \${MAX_WAIT}s)" >&2
      return 1
    fi
    echo "[swift-vnet] \${desc}: attempt \${attempt} failed (elapsed \${elapsed}s/\${MAX_WAIT}s; likely container DNS cold-start or RBAC propagation); retrying in \${POLL_INTERVAL}s..." >&2
    sleep "\${POLL_INTERVAL}"
    attempt=\$((attempt + 1))
  done
  if [ "\${attempt}" -gt 1 ]; then
    echo "[swift-vnet] \${desc}: succeeded on attempt \${attempt} (\$((SECONDS - start))s)" >&2
  fi
}

# Resolver check via python3 (bundled with the azure-cli image, unlike getent which is not
# guaranteed present) — this also exercises the exact getaddrinfo() path that fails with
# EAI_AGAIN during the DNS cold-start we are gating on.
resolves() { python3 -c "import socket,sys; socket.getaddrinfo(sys.argv[1], None)" "\$1" >/dev/null 2>&1; }

# DNS readiness gate: wait until the container's resolver can actually resolve the Azure
# control-plane endpoints before running any az command (see mode 1 above). This turns the
# cold-start into an explicit, well-logged wait on the real dependency instead of a noisy
# az login stack trace.
echo "[swift-vnet] Waiting for DNS readiness (Azure control-plane endpoints)..."
retry "dns readiness (login.microsoftonline.com)" resolves login.microsoftonline.com
retry "dns readiness (management.azure.com)" resolves management.azure.com

# Log in as globalMSI. Wrapped in retry as cheap insurance against a residual transient
# (TLS/outbound blip or IMDS not ready) even after DNS is up.
echo "[swift-vnet] Logging in with the Swift-registered managed identity..."
retry "az login (globalMSI)" az login --identity --username "${DEPLOYMENT_MSI_ID}" --output none
retry "az account set" az account set --subscription "${SUBSCRIPTION_ID}"

# Wait until the managed identity can actually read the resource group before deciding whether
# the VNet exists (see mode 2 above: RBAC is eventually consistent, so without this gate a
# transient auth failure on the existence check could misroute a re-run into the create path).
retry "az group show" az group show --name "${RESOURCE_GROUP}" --output none

# Decide whether the VNet already exists, distinguishing a genuine NotFound (create path) from
# a transient/AuthorizationFailed error (retry the read). The az group show gate above already
# waits for RBAC to propagate, but we still never want a transient read failure to misroute an
# existing VNet into the create path - only a real NotFound should reach create.
vnet_exists=""
show_start=\${SECONDS}
show_attempt=1
while true; do
  if show_err=\$(az network vnet show --resource-group "${RESOURCE_GROUP}" --name "${VNET_NAME}" --output none 2>&1); then
    vnet_exists="yes"
    break
  fi
  if printf '%s' "\${show_err}" | grep -qiE "ResourceNotFound|was not found|could not be found"; then
    vnet_exists="no"
    break
  fi
  show_elapsed=\$((SECONDS - show_start))
  if [ "\${show_elapsed}" -ge "\${MAX_WAIT}" ]; then
    echo "[swift-vnet] vnet existence check: giving up after \${show_attempt} attempt(s) / \${show_elapsed}s: \${show_err}" >&2
    exit 1
  fi
  echo "[swift-vnet] vnet existence check: attempt \${show_attempt} non-NotFound failure (elapsed \${show_elapsed}s/\${MAX_WAIT}s; likely transient/RBAC propagation); retrying in \${POLL_INTERVAL}s..." >&2
  sleep "\${POLL_INTERVAL}"
  show_attempt=\$((show_attempt + 1))
done

if [ "\${vnet_exists}" = "yes" ]; then
  echo "[swift-vnet] VNet ${VNET_NAME} exists. Updating Swift tag..."
  retry "az resource tag" az resource tag \
    --is-incremental \
    --tags stampcreatorserviceinfo=true \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${VNET_NAME}" \
    --resource-type Microsoft.Network/virtualNetworks \
    --api-version 2024-05-01
else
  echo "[swift-vnet] VNet ${VNET_NAME} does not exist. Creating..."
  retry "az network vnet create" az network vnet create \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${VNET_NAME}" \
    --address-prefixes "${VNET_ADDRESS_PREFIX}" \
    --tags stampcreatorserviceinfo=true
fi

echo "[swift-vnet] Swift VNet ${VNET_NAME} is ready and tagged."
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
last_state="__init__"
last_log=0
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
  # Log on state transitions (plus the initial state) and emit a periodic heartbeat so long
  # stretches in "Running" still produce output — this avoids CI/EV2 inactivity/no-output
  # timeouts during multi-minute cold starts without flooding the step with identical lines.
  now=$((SECONDS - start))
  if [ "${state}" != "${last_state}" ] || [ $((now - last_log)) -ge "${HEARTBEAT_INTERVAL}" ]; then
    echo "Waiting for ${CONTAINER_GROUP_NAME} to finish (state: ${state:-pending}, elapsed ${now}s)..."
    last_state="${state}"
    last_log=${now}
  fi
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
