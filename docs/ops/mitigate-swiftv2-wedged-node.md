# Mitigation: SWIFT v2 Delegated-NIC Wedged Node

## Overview

Azure SWIFT v2 management-cluster nodes can enter a failure state where every pod scheduled to the node fails with `FailedCreatePodSandBox`. The root cause is an Azure platform issue (Cloudnet/RNC) where the delegated FrontendNIC virtual function (VF) is torn down and re-added at the platform level just after CNI attach, leaving the node's network stack broken. The CNI errors are the symptom, not the cause.

Two things to know before you start digging:

- **CNS is not the culprit, so do not spend time there.** On the cases we have analysed, `azure-cns` assigned and released the pod's IP cleanly on every single retry with `ReturnCode:Success`, and the `MultitenantPodNetworkConfig` resolved a secondary IP within about a second of the pod being scheduled. IP pool exhaustion is ruled out too. The fault is in the `azure-vnet` CNI plugin's endpoint-creation step, which consumes the CNS response and programs the interface into the pod netns.
- **`azure-vnet` has no logs in Kusto.** It runs as a host binary invoked by containerd, not as a pod, so nothing it emits is ingested into ServiceLogs. That is exactly why the log-collection script in the appendix exists: if Cloudnet/RNC want evidence, it has to be pulled off the node before you recycle it.

The node will not self-heal. The standing mitigation is to **cordon and delete the wedged VMSS instance** so pods reschedule onto healthy nodes and AKS replaces the instance.

## Tracking

- **ICM**: 832382845 (Azure-owned, Cloudnet/RNC root cause)
- **Past occurrences**:
  - `prod uksouth` — node `aks-userswft2-40171262-vmss000004`, ~18h of continuous failure, ~2,584 events (AROSLSRE-1524)
  - `prod australiaeast` — node `aks-userswft2-40474110-vmss00000f`, ~3 days of continuous failure, ~2,419 events (AROSLSRE-1563 / AROSLSRE-1585)
  - `prod centralindia`, node `aks-userswft3-38105468-vmss000005`, ongoing since 2026-08-01, ~1,074 events, took out three consecutive prod e2e runs on 2026-08-04 (AROSLSRE-1717)

## Symptoms

All of these will be present on the affected node:

| Signal | Detail |
|---|---|
| **Event** | `FailedCreatePodSandBox` at ~15s retry cadence, thousands of occurrences |
| **Error messages** | `route ip+net: no such network interface`, `mtpnc is not ready`, `dhcp discover timed out` |
| **Blast radius** | 100% of pods scheduled to the node fail; hosted-cluster router replicas are especially impacted → `KASLoadBalancerNotReachable` → cluster-create timeouts |
| **Isolation** | Failures usually concentrate on one node, but a short cluster-wide burst across several nodes is possible (see AROSLSRE-1717). Confirm with per-node event counts rather than assuming a single node |
| **Duration** | Does not self-heal; persists for days until the node is deleted |

## Diagnosis

### 0. Quick check: node-health controller labels

If the `node-health` controller is deployed on the cluster (see [Appendix: Automated Detection](#appendix-automated-detection-node-health-controller)), the quickest first pass is:

```bash
kubectl get nodes -l node-health.aro-hcp.azure.com/status=wedged
kubectl get node <node-name> -o jsonpath='{.metadata.annotations}' | jq
```

Fall back to the Kusto queries below if the label is not present (e.g. on a cluster that has not picked up the controller yet).

### 1. Identify the wedged node via Kusto (hcp-prod-{region})

```kql
// Look for sustained FailedCreatePodSandBox, per node.
// Run against the region's cluster, database ServiceLogs.
kubernetesEvents
| where timestamp > ago(7d)
| where reason == "FailedCreatePodSandBox"
| where message has "no such network interface"
     or message has "mtpnc is not ready"
     or message has "dhcp discover"
| summarize events=count(), nsAffected=dcount(eventNamespace), podsAffected=dcount(objectName),
            firstSeen=min(timestamp), lastSeen=max(timestamp)
  by cluster, host
| order by events desc
```

### 2. Distinguish hard-wedge from transient flap

This is the critical decision gate. Not every occurrence of these errors requires node deletion. The same Azure fault (ICM 832382845) has two modes:

| Mode | Behavior | Action |
|---|---|---|
| **Hard-wedge** | Zero successful sandbox creations, continuous error storm (2500-5300+ events), VF gone, never recovers | Cordon + delete node |
| **Transient flap** | Bounded error bursts with successful DHCP sends interleaved, self-heals within minutes to hours | Alert only, do not recycle |

```kql
// Flap vs. wedge: check for interleaved successes on the suspect node.
// If SuccessCount > 0, the node is flapping (self-healing). Do NOT delete.
// If SuccessCount == 0 for >= 30-60 min of continuous errors, it is wedged.
let suspect_node = "<node-name>";
let suspect_cluster = "<mgmt-cluster-name>";
let lookback = 2h;
kubernetesEvents
| where timestamp > ago(lookback)
| where cluster == suspect_cluster and host == suspect_node
| where reason in ("FailedCreatePodSandBox", "Created", "Started")
| summarize
    FailCount = countif(reason == "FailedCreatePodSandBox"
                        and (message has "no such network interface"
                             or message has "mtpnc is not ready"
                             or message has "dhcp discover")),
    SuccessCount = countif(reason in ("Created", "Started")),
    FirstFail = minif(timestamp, reason == "FailedCreatePodSandBox"),
    LastFail = maxif(timestamp, reason == "FailedCreatePodSandBox"),
    LastSuccess = maxif(timestamp, reason in ("Created", "Started"))
    by host
| extend FailDuration = LastFail - FirstFail,
         TimeSinceLastSuccess = LastFail - LastSuccess
| project host, FailCount, SuccessCount, FailDuration,
          TimeSinceLastSuccess, FirstFail, LastFail, LastSuccess
```

> **Decision rule** (from Rael's analysis):
> - 0 successful sandbox creations for >= 30-60 min of continuous route errors → **wedge** → proceed with cordon + delete
> - Any successes interleaved with the failures → **flap** → alert only, do not recycle
>
> Both are queryable in `hcp-prod-{region}` ServiceLogs.

### 3. Confirm the node is still failing (kubectl)

```bash
kubectl get events --field-selector reason=FailedCreatePodSandBox,involvedObject.kind=Pod \
  --sort-by='.lastTimestamp' | grep <node-name> | tail -20
```

### 4. Check whether AKS node auto-repair has already attempted remediation

AKS management clusters have built-in node auto-repair enabled by default. Before manually intervening, check whether AKS has already tried its escalation sequence (reboot, reimage, redeploy, delete). The SWIFT v2 VF churn issue is **not resolved by AKS auto-repair** because the node typically stays `Ready` (kubelet is fine; only pod sandbox creation fails), so auto-repair may not trigger at all.

```bash
# Check for auto-repair events on the node
kubectl get events --field-selector involvedObject.name=<node-name> \
  --sort-by='.lastTimestamp' | grep -E 'NodeReboot|NodeReimage|NodeRedeploy'

# Check node conditions -- if the node shows Ready, AKS auto-repair
# will not act (it only triggers on NotReady nodes)
kubectl get node <node-name> -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.lastTransitionTime}{"\n"}{end}'
```

> **Key point**: If the node is `Ready` and no auto-repair events exist, AKS will not self-heal this issue. Proceed with manual mitigation. If auto-repair events ARE present but the node is still wedged (events show `NodeRedeployError` or similar), the platform-level VF issue persists across reimages and manual deletion is still required.

### 5. Check `recreate-system-pool` self-healer (system pool nodes only)

If the wedged node is in the **system pool** (`aks-system-*`), the `recreate-system-pool` SDP pipeline may already be handling remediation. This tool detects NRP-KVS failure storms and performs a full system-pool recreation (create temp pool, cordon/drain, delete broken pool, recreate, migrate back). Check its status before taking manual action.

```bash
# Check if the system pool is already in a wedged/Updating state
# (the self-healer keys on provisioningState=Updating + NRP-KVS error storm)
az aks nodepool show \
  --resource-group <resource-group> \
  --cluster-name <cluster-name> \
  --name system \
  --query '{state: provisioningState, count: count}' -o table

# Check for NRP-KVS (NetworkingInternalOperationError) events in the last 6h
# If the self-healer's guards are passing, it will handle the remediation
```

> **Note**: The `recreate-system-pool` self-healer targets NRP-KVS wedge scenarios specifically; it may not cover the SWIFT v2 VF churn issue if the system pool's `provisioningState` is `Succeeded` (not `Updating`). If the self-healer is not applicable, proceed with manual mitigation.

### 6. Confirm router impact (if cluster-creates are timing out)

```bash
# Check if router replicas are landing on the wedged node
kubectl get pods -A -o wide | grep router | grep <node-name>
```

## Prerequisites

- JIT access to the affected management-cluster AKS (include this runbook link and ICM 832382845 in the justification)
- `kubectl` configured against the management AKS
- (Optional) The ACN log-collection script if Cloudnet/RNC wants on-node evidence before recycling

## Mitigation Procedure

### Step 1: (Optional) Collect ACN logs for evidence preservation

Run the log-collection script **before** deleting the node, in case the Azure networking team (Cloudnet/RNC) needs on-node state for root-cause analysis.

```bash
# Target only the wedged node
NODES_FILTER='<vmss-instance-id>' ./collect-swiftv2-node-logs.sh
```

The script is read-only: it creates temporary busybox debug pods, pulls `/host/var/log/azure-vnet*` and `/host/var/run/azure-cns` state, tars them, and deletes only the debug pods it created. See the full script in the appendix below or ask the on-call SRE for the latest version.

### Step 2: Cordon the node

```bash
kubectl cordon <node-name>
```

This prevents new pods from being scheduled to the node while you verify and delete.

### Step 3: Delete the VMSS instance

```bash
# Get the VMSS name and instance ID from the node's provider ID
PROVIDER_ID=$(kubectl get node <node-name> -o jsonpath='{.spec.providerID}')
echo "${PROVIDER_ID}"
# Format: azure:///subscriptions/.../virtualMachineScaleSets/<vmss-name>/virtualMachines/<instance-id>

# Delete via az cli (the VMSS will automatically provision a replacement)
az vmss delete-instances \
  --resource-group <node-resource-group> \
  --name <vmss-name> \
  --instance-ids <instance-id>
```

Alternatively, if you only have `kubectl` access:

```bash
kubectl delete node <node-name>
```

> **Note**: Deleting the node object from Kubernetes does not delete the underlying VMSS instance. For a full cleanup, use `az vmss delete-instances`. The AKS cluster autoscaler or node-pool reconciler will provision a replacement.

> **This mitigation is not durable.** The fault has been observed moving to a different instance in the same pool, sometimes within minutes of the previous one being recycled (AROSLSRE-1717). Recycling clears the immediate incident, it does not fix the underlying platform issue. Update ICM 832382845 on every recurrence so Azure has the pattern.

### Step 4: Verify recovery

```bash
# Confirm the node is gone
kubectl get nodes | grep <node-name>

# Confirm pods are rescheduling to healthy nodes
kubectl get pods -A --field-selector status.phase!=Running,status.phase!=Succeeded | head -20

# Confirm no new FailedCreatePodSandBox events on other nodes
kubectl get events --field-selector reason=FailedCreatePodSandBox --sort-by='.lastTimestamp' | tail -10
```

## Post-Mitigation

### 1. Verify no new sandbox failures (Kusto)

```kql
// Run against the region's cluster, database ServiceLogs, after node deletion.
// Expect 0 results for the deleted node, and no new nodes showing the same pattern.
kubernetesEvents
| where timestamp > ago(30m)
| where reason == "FailedCreatePodSandBox"
| where message has "no such network interface"
     or message has "mtpnc is not ready"
     or message has "dhcp discover"
| summarize events=count(), lastSeen=max(timestamp) by cluster, host
| order by events desc
```

### 2. Verify cluster-creates resume

If the wedged node was causing `KASLoadBalancerNotReachable` timeouts, confirm that hosted-cluster router replicas are now healthy and new cluster creates succeed.

```bash
# Check router pods are running on healthy nodes
kubectl get pods -A -o wide | grep router | grep -v Completed

# Check for KASLoadBalancerNotReachable alerts clearing
kubectl get events -A --sort-by='.lastTimestamp' | grep KASLoadBalancer | tail -5
```

### 3. Update tracking

- **Update the ICM** (832382845) with the occurrence details (region, node name, duration, event count) so the Azure networking team has the data for root-cause resolution.
- **File or update the SRE ticket** (e.g., AROSLSRE-series) with the mitigation details and any collected ACN logs.

## Distinguishing from Other Node Failures

| Condition | This issue (SWIFT v2 VF churn) | General node NotReady |
|---|---|---|
| Node status | Usually `Ready` (kubelet is fine) | `NotReady` |
| Failure mode | Only pod sandbox creation fails | All node operations impacted |
| Error signature | `no such network interface` / `mtpnc` / `dhcp discover` | Varies |
| AKS auto-repair triggers? | **No** -- node is `Ready`, so auto-repair does not engage | Yes -- AKS escalates reboot, reimage, redeploy, delete |
| Self-healing | Never (manual deletion required) | Often recovers via auto-repair |
| Node pool | `userswft2` / `userswft3` (SWIFT v2 pools) | Any pool |

## Appendix: Automated Detection (node-health controller)

The criteria below came out of the first observed self-heal (AROSLSRE-1642/1643) and are now implemented by the `node-health` controller in mgmt-agent (AROSLSRE-1588):

1. **Key on concurrent success on the node**, do not wait passively for self-heal.
2. **0 successful sandbox creations for >= 30-60 min of continuous route errors** → wedge → cordon + delete.
3. **Any successes interleaved with the failures** → flap → alert only, do not recycle.

Where it has rolled out, a wedged node carries:

- label `node-health.aro-hcp.azure.com/status=wedged`
- annotations `node-health.aro-hcp.azure.com/detector`, `/reason`, `/signature`, `/observed-at`
- metrics `nodehealth_node_wedged`, `nodehealth_detections_total`, `nodehealth_label_actions_total`

So the quickest first pass at diagnosis is:

```bash
kubectl get nodes -l node-health.aro-hcp.azure.com/status=wedged
kubectl get node <node-name> -o jsonpath='{.metadata.annotations}' | jq
```

The controller only labels and annotates, it does not cordon, drain or delete. The mitigation steps in this runbook are still manual. Fall back to the Kusto queries above if the label is not present, for example on a cluster that has not picked up the controller yet.

## Appendix: ACN Log-Collection Script

```bash
#!/usr/bin/env bash
# ICM 832382845 - SWIFT v2 delegated-NIC (VF) churn log collection.
# Read-only: creates its own busybox debug pods, pulls
# /host/var/log/azure-vnet* + /host/var/run/azure-cns state,
# tars them, and deletes ONLY the debug pods it created.
#
# Run under JIT against the affected mgmt AKS (kubeconfig already pointed at it).
# Optional: set NODES_FILTER to a grep pattern to target specific node(s), e.g.
#   NODES_FILTER='vmss000004' ./collect-swiftv2-node-logs.sh
# Default (unset) collects from ALL nodes.

set -uo pipefail
set -x

OUTPUT_DIR="$(mktemp -d)"
TARBALL="node-logs-$(date +%Y%m%dT%H%M%S).tar.gz"
IMAGE="busybox"
PODS_TO_DELETE=()

cleanup() {
  echo "Cleaning up debug pods..."
  for pod in "${PODS_TO_DELETE[@]+"${PODS_TO_DELETE[@]}"}"; do
    kubectl delete pod "${pod}" --ignore-not-found 2>/dev/null &
  done
  wait
}
trap cleanup EXIT

echo "Collecting logs into ${OUTPUT_DIR} ..."

mapfile -t nodes < <(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' \
  | { if [[ -n "${NODES_FILTER:-}" ]]; then grep -E "${NODES_FILTER}"; else cat; fi; })

# Create debug pods sequentially (wait/collection parallelised below)
declare -A node_pods
for node in "${nodes[@]}"; do
  create_output=$(kubectl debug "node/${node}" --image "${IMAGE}" -- sleep 3600 2>&1)
  echo "${create_output}"
  pod=$(echo "${create_output}" | grep -oP 'pod[/ ]\K[a-z0-9][-a-z0-9]*' | head -1)
  if [[ -n "${pod}" ]]; then
    node_pods["${node}"]="${pod}"
    PODS_TO_DELETE+=("${pod}")
  else
    echo "WARNING: could not determine debug pod name for ${node}"
  fi
done

# Wait for all pods to be ready in parallel
for node in "${!node_pods[@]}"; do
  kubectl wait --for=condition=Ready "pod/${node_pods[${node}]}" --timeout=120s &
done
wait

# Collect logs from all nodes in parallel
collect_node() {
  local node="$1"
  local pod="$2"
  local node_dir="${OUTPUT_DIR}/${node}"
  mkdir -p "${node_dir}/var-log" "${node_dir}/var-run-azure-cns"

  echo "  [${node}] Collecting azure-vnet logs..."
  if ! kubectl exec "${pod}" -- sh -c 'cd /host/var/log && tar cf - azure-vnet* 2>/dev/null' \
    | tar xf - -C "${node_dir}/var-log" 2>/dev/null; then
    echo "  [${node}] (no azure-vnet logs found)"
  fi

  echo "  [${node}] Collecting azure-cns state..."
  if ! kubectl exec "${pod}" -- sh -c 'cd /host/var/run/azure-cns && tar cf - . 2>/dev/null' \
    | tar xf - -C "${node_dir}/var-run-azure-cns" 2>/dev/null; then
    echo "  [${node}] (no azure-cns state found)"
  fi

  echo "  [${node}] Done."
}

for node in "${!node_pods[@]}"; do
  collect_node "${node}" "${node_pods[${node}]}" &
done
wait

echo "Creating tarball ${TARBALL} ..."
tar czf "${TARBALL}" -C "${OUTPUT_DIR}" .
rm -rf "${OUTPUT_DIR}"
echo "Done: ${TARBALL}"
```
