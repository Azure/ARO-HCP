# Mitigation: SWIFT v2 Delegated-NIC Wedged Node

## Overview

Azure SWIFT v2 management-cluster nodes can enter a failure state where every pod scheduled to the node fails with `FailedCreatePodSandBox`. The root cause is an Azure platform issue (Cloudnet/RNC) where the delegated FrontendNIC virtual function (VF) is torn down and re-added at the platform level just after CNI attach, leaving the node's network stack broken. The CNI errors are the symptom, not the cause.

Two things to know before you start digging:

- **CNS is not the culprit, so do not spend time there.** On the cases we have analysed, `azure-cns` assigned and released the pod's IP cleanly on every single retry with `ReturnCode:Success`, and the `MultitenantPodNetworkConfig` resolved a secondary IP within about a second of the pod being scheduled. IP pool exhaustion is ruled out too. The fault is in the `azure-vnet` CNI plugin's endpoint-creation step, which consumes the CNS response and programs the interface into the pod netns.
- **`azure-vnet` has no logs in Kusto.** It runs as a host binary invoked by containerd, not as a pod, so nothing it emits is ingested into ServiceLogs. That is exactly why the log-collection script in the appendix exists: if Cloudnet/RNC want evidence, it has to be pulled off the node before you recycle it.

The node will not self-heal in the hard-wedge variant. In the intermittent flapping variant (AROSLSRE-1717), the node alternates between healthy and failing states, self-resolving between bursts but recurring under pod scheduling pressure. The standing mitigation for both variants is to **cordon and delete the VMSS instance** so pods reschedule onto healthy nodes and AKS replaces the instance.

## Tracking

- **ICM**: 832382845 (Azure-owned, Cloudnet/RNC root cause)
- **Past occurrences**:
  - `prod uksouth` — node `aks-userswft2-40171262-vmss000004`, ~18h of continuous failure, ~2,584 events (AROSLSRE-1524)
  - `prod australiaeast` — node `aks-userswft2-40474110-vmss00000f`, ~3 days of continuous failure, ~2,419 events (AROSLSRE-1563 / AROSLSRE-1585)
  - `prod centralindia` — node `aks-userswft3-38105468-vmss000005`, intermittent flapping since 2026-08-01, ~1,074 events, took out three consecutive prod e2e runs on 2026-08-04; node deleted 2026-08-04 via `kubectl delete node` (AROSLSRE-1717 / AROSLSRE-1723)

## Symptoms

All of these will be present on the affected node:

| Signal | Detail |
|---|---|
| **Event** | `FailedCreatePodSandBox` at ~15s retry cadence, thousands of occurrences |
| **Error messages** | `route ip+net: no such network interface`, `mtpnc is not ready`, `dhcp discover timed out` |
| **Blast radius** | Every pod needing a delegated NIC fails on that node, which in practice is the whole hosted-control-plane workload scheduled there; hosted-cluster router replicas are especially impacted → `KASLoadBalancerNotReachable` → cluster-create timeouts |
| **Isolation** | Failures usually concentrate on one node, but a short cluster-wide burst across several nodes is possible (see AROSLSRE-1717). Confirm with per-node event counts rather than assuming a single node |
| **Duration** | Hard-wedge does not self-heal; persists for days until the node is deleted. Intermittent flap variant self-resolves between bursts but recurs under pod scheduling pressure (see AROSLSRE-1717) |

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

### 3. Confirm the node is still failing

Kubernetes events have a short TTL (~1h). If the wedge is intermittent (flapping variant), kubectl events may show nothing even though the fault is still latent.

**kubectl (if events are recent):**

```bash
kubectl get events -A --field-selector reason=FailedCreatePodSandBox | grep <node-name> | tail -20
```

**Kusto (reliable regardless of event TTL):**

```kql
kubernetesEvents
| where timestamp > ago(4h)
| where host == "<node-name>"
| where reason == "FailedCreatePodSandBox"
| order by timestamp desc
| take 20
```

**Test pod (confirms active vs. latent fault):**

If Kusto shows recent failures but kubectl events are empty, schedule a test pod directly on the suspect node to check whether the NIC is currently functional:

```bash
kubectl run nic-test --image=mcr.microsoft.com/cbl-mariner/base/core:2.0 \
    --overrides='{"spec":{"nodeName":"<node-name>"}}' \
    --command -- sleep 300
kubectl get pod nic-test -w
```

- If the pod reaches `Running`: this is inconclusive, not a clean bill of health. The probe pod has no delegated NIC, so it exercises the ordinary CNI path and not the `SecondaryEndpointClient` path that fails here. Decide from the flap-vs-wedge query (Step 2), not from this pod.
- If the pod stays in `ContainerCreating` with `FailedCreatePodSandBox`: the NIC is actively wedged. Proceed with mitigation.

Clean up after: `kubectl delete pod nic-test`

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

### JIT Role Selection

| Method | JIT Role | Notes |
|---|---|---|
| `kubectl delete node` | **Azure Kubernetes Service RBAC Cluster Admin** on the target subscription | Removes the Kubernetes node object. AKS node-pool reconciler detects the missing node, deprovisions the underlying VMSS instance, and provisions a replacement. Confirmed working on prod centralindia (AROSLSRE-1723). This is the normal JIT role used by the Service Life Cycle team. |
| `az vmss delete-instances` | **Change Safety Contributor** on the target subscription | Deletes the VMSS instance directly via ARM; AKS provisions a replacement. Confirmed working on australiaeast (`87faad51-...`, AROSLSRE-1563). On centralindia (AROSLSRE-1723), both AKS RBAC Cluster Admin and Change Safety Contributor failed to grant `az vmss delete-instances` permission — the required ARM role for this subscription needs further investigation. |
| Geneva action (ARM) | **Change Safety Contributor** on the target subscription | Use the Geneva Actions portal ([portal.microsoftgeneva.com/actions](https://portal.microsoftgeneva.com/actions)): **Azure Resource Manager > Resource Group Management > Delete resource and resource group data in a location for a subscription**. Target the specific VMSS instance resource ID. Alternative to `az vmss delete-instances` when CLI access is unavailable. |

> **Note on the centralindia incident (AROSLSRE-1723)**: Both **AKS RBAC Cluster Admin** and **Change Safety Contributor** were granted via JIT. Neither was sufficient for `az vmss delete-instances` on this subscription. The mitigation succeeded via `kubectl delete node` instead — the node object was removed immediately, AKS deprovisioned the VMSS instance, and a replacement node was provisioned ~15 minutes later (reaching Ready ~40 seconds after creation).

## Mitigation Procedure

### Step 1: (Optional) Collect ACN logs for evidence preservation

Run the log-collection script **before** deleting the node, in case the Azure networking team (Cloudnet/RNC) needs on-node state for root-cause analysis. The script lives at [`hack/collect-swiftv2-node-logs.sh`](../../hack/collect-swiftv2-node-logs.sh).

```bash
# Target only the wedged node (run from the repo root)
NODES_FILTER='<node-name>' ./hack/collect-swiftv2-node-logs.sh
```

The script is read-only: it creates temporary debug pods, pulls `/host/var/log/azure-vnet*` and `/host/var/run/azure-cns` state, tars them, and deletes only the debug pods it created.

### Step 2: Cordon the node

```bash
kubectl cordon <node-name>
```

This prevents new pods from being scheduled to the node while you verify and delete.

### Step 3: Drain the node

```bash
kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data --timeout=5m
```

Draining evicts remaining pods gracefully, consulting PodDisruptionBudgets and giving workloads time to terminate cleanly. Skipping this step means the subsequent node deletion force-kills pods without PDB checks.

> **Note**: On a fully wedged node most pods are already in `CrashLoopBackOff` / `ContainerCreating`, so drain may have little to evict. It is still worth running for the few pods (e.g. daemonsets excluded by `--ignore-daemonsets`) that may still be healthy.

### Step 4: Delete the VMSS instance

**Option A: `az vmss delete-instances` (preferred)**

```bash
# Get the VMSS name and instance ID from the node's provider ID
PROVIDER_ID=$(kubectl get node <node-name> -o jsonpath='{.spec.providerID}')
echo "${PROVIDER_ID}"
# Format: azure:///subscriptions/<subscription-id>/resourceGroups/<node-resource-group>/providers/Microsoft.Compute/virtualMachineScaleSets/<vmss-name>/virtualMachines/<instance-id>

# Delete via az cli (the VMSS will automatically provision a replacement)
az vmss delete-instances \
  --resource-group <node-resource-group> \
  --name <vmss-name> \
  --instance-ids <instance-id>
```

**Option B: `kubectl delete node` (fallback)**

If `az vmss delete-instances` fails due to insufficient JIT permissions (see [JIT Role Selection](#jit-role-selection)):

```bash
kubectl delete node <node-name>
```

This removes the Kubernetes node object. The AKS node-pool reconciler will detect the missing node, deprovision the underlying VMSS instance, and provision a replacement. This was confirmed working on prod centralindia (AROSLSRE-1723) where Change Safety Contributor was insufficient for direct VMSS deletion.

> **This mitigation is not durable.** The fault has been observed moving to a different instance in the same pool, sometimes within minutes of the previous one being recycled (AROSLSRE-1717). Recycling clears the immediate incident, it does not fix the underlying platform issue. Update ICM 832382845 on every recurrence so Azure has the pattern.

### Step 5: Verify recovery

```bash
# Confirm the old node is gone and a replacement is coming up
kubectl get nodes | grep <vmss-pool-prefix>

# Confirm pods are rescheduling to healthy nodes
kubectl get pods -A --field-selector status.phase!=Running,status.phase!=Succeeded | head -20

# Confirm no new FailedCreatePodSandBox events on other nodes
kubectl get events -A --field-selector reason=FailedCreatePodSandBox --sort-by='.lastTimestamp' | tail -10
```

> **Expected transient errors on the replacement node**: When the new VMSS instance joins the cluster, daemonset pods (e.g. `arobit-forwarder`) may briefly show `FailedCreatePodSandBox` with `Pod "..." not found`. This is a benign CNS cache sync race on the freshly provisioned node and self-resolves within 1-2 minutes. Do not confuse this with a recurrence of the SWIFT v2 wedge — check the error message: the wedge signature is `no such network interface` / `mtpnc is not ready` / `dhcp discover timed out`, not `Pod not found`.

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
| Self-healing | Hard-wedge: never. Flap: self-resolves between bursts but recurs (manual deletion still recommended) | Often recovers via auto-repair |
| Node pool | `userswft2` / `userswft3` (SWIFT v2 pools) | Any pool |

## Lessons Learned

Collected from operational incidents AROSLSRE-1524, 1563, 1604, 1717, 1723.

### kubectl events have a ~1h TTL

Do not rely on `kubectl get events` alone to confirm or rule out the wedge. If the last failure burst was more than ~1h ago, events will be empty even though the fault is still latent. Always cross-reference with Kusto (`kubernetesEvents` in `ServiceLogs`), which retains data for days.

### The fault can be intermittent (flapping variant)

AROSLSRE-1717 (centralindia) introduced a variant not seen in uksouth or australiaeast: the node alternates between healthy and failing states. Bounded error bursts (2-11 min) occur under pod scheduling pressure (e.g. e2e runs), then self-resolve when load drops. A test pod scheduled in a quiet period will succeed, but the fault recurs on the next burst. The flap-vs-wedge Kusto query (Diagnosis Step 2) is essential for deciding whether to delete.

### `kubectl delete node` works as a fallback

When `az vmss delete-instances` fails due to insufficient JIT permissions, `kubectl delete node <node-name>` is a viable alternative. The AKS node-pool reconciler detects the missing node object, deprovisions the underlying VMSS instance, and provisions a replacement. Confirmed on prod centralindia (AROSLSRE-1723): the node object was removed immediately, a replacement VMSS instance was provisioned ~15 minutes later, and the new node reached `Ready` ~40 seconds after creation.

### Transient CNS errors on replacement nodes are benign

When a new VMSS instance joins the cluster, daemonset pods (especially `arobit-forwarder`) may briefly show `FailedCreatePodSandBox` with `Pod "..." not found`. This is a CNS cache sync race on the freshly provisioned node, not a recurrence of the SWIFT v2 wedge. It self-resolves within 1-2 minutes. Check the error message to distinguish: the wedge signature is `no such network interface` / `mtpnc is not ready` / `dhcp discover timed out`.

### `az vmss delete-instances` may fail even with expected JIT roles

On AROSLSRE-1723 (centralindia), both **Azure Kubernetes Service RBAC Cluster Admin** and **Change Safety Contributor** were granted via JIT. Neither was sufficient for `az vmss delete-instances` on the target subscription. The required ARM role for VMSS instance deletion needs further investigation. The mitigation succeeded via `kubectl delete node` instead. For future occurrences, try `kubectl delete node` first (works with AKS RBAC Cluster Admin) and fall back to Geneva action if needed.

### SAW Cloud Shell quirks

- Multi-line commands with `|` can fail with `command not found`. Paste commands as single lines or type them manually.
- `jsonpath` with curly braces and tabs/newlines may fail if pasted with smart quotes. Use `-o json` and pipe to `jq` as a fallback.

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

The script is maintained as a standalone file: [`hack/collect-swiftv2-node-logs.sh`](../../hack/collect-swiftv2-node-logs.sh).

Usage:

```bash
# Collect from a specific node (run from the repo root)
NODES_FILTER='vmss000004' ./hack/collect-swiftv2-node-logs.sh

# Collect from all nodes (default)
./hack/collect-swiftv2-node-logs.sh
```

The script is read-only and self-cleaning: it creates temporary busybox debug pods, copies `azure-vnet` logs and `azure-cns` state from the host filesystem, produces a timestamped tarball, and deletes only the debug pods it created.
