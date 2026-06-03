---
name: hcp-deletion-dataplane
description: Troubleshoot HCP cluster deletion issues on the customer data plane. Use this when worker nodes are not draining, pods are stuck terminating, or PVCs are blocking deletion on the customer cluster.
---

## Goal
Diagnose data plane issues during HCP cluster deletion — stuck pods, nodes not draining, PVCs blocking cleanup.

## Important Instructions
- Do NOT check `kubectl config current-context` or kubeconfig availability. You are running in-cluster with a ServiceAccount — kubectl works automatically.
- Do NOT ask the user for kubeconfig, context, or cluster access. Just run the kubectl commands directly.
- When checking resources, always use `-o wide` for more context.
- Keep output concise — summarize findings.

## Workflow

### Step 1: Check Node Status
* Run: `kubectl get nodes -o wide`
* Check if nodes are being cordoned/drained (SchedulingDisabled)
* If nodes show Ready + SchedulingDisabled → drain in progress
* If nodes show NotReady → nodes already terminated

### Step 2: Check Pods Stuck Terminating
* Run: `kubectl get pods -A --field-selector=status.phase=Running -o wide | head -30`
* Run: `kubectl get pods -A -o json | jq '.items[] | select(.metadata.deletionTimestamp != null) | {ns: .metadata.namespace, name: .metadata.name, deletionTimestamp: .metadata.deletionTimestamp, finalizers: .metadata.finalizers}'` 2>/dev/null
* Pods with `deletionTimestamp` set but still running are stuck terminating
* Check if they have finalizers blocking deletion

### Step 3: Check PVCs and PVs
* Run: `kubectl get pvc -A`
* Run: `kubectl get pv`
* PVCs in Bound state with `deletionTimestamp` set are stuck
* PVs in Released state may need manual cleanup

### Step 4: Check Namespaces Stuck Terminating
* Run: `kubectl get ns | grep Terminating`
* For each: `kubectl get ns <ns> -o json | jq '.status.conditions'`
* Check for `FinalizersRemaining` — which resources have finalizers?

### Step 5: Check Warning Events
* Run: `kubectl get events -A --sort-by=.lastTimestamp --field-selector type=Warning | tail -20`

## Synthesize Findings

* **Nodes SchedulingDisabled** → drain in progress, may just need time
* **Pods stuck with finalizers** → specific operator holding finalizer, check operator logs
* **PVCs stuck** → storage driver not releasing volumes (Azure disk detach issue)
* **Namespaces Terminating** → resources with finalizers blocking, check which resources remain
* **No nodes found** → nodes already terminated, data plane cleanup likely complete

## Recommended Remediation Steps
* Stuck pods: force delete with `kubectl delete pod <name> -n <ns> --grace-period=0 --force`
* Stuck PVCs: remove finalizer on PVC, then delete
* Namespace stuck: identify and remove finalizers on blocking resources
* If data plane is already gone (no API server): this is expected — deletion proceeds from controlplane side
