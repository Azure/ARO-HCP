---
name: hcp-deletion-serviceplane
description: Troubleshoot HCP cluster deletion stuck on the service cluster. Use this when a cluster deletion is stuck in Deleting state, or when the backend operation is not progressing, or Maestro ResourceBundles are not being cleaned up.
---

## Goal
Diagnose why an ARO-HCP cluster deletion is stuck by investigating the service cluster components involved in the deletion workflow.

## Important Instructions
- Do NOT check `kubectl config current-context` or kubeconfig availability. You are running in-cluster with a ServiceAccount — kubectl works automatically.
- Do NOT ask the user for kubeconfig, context, or cluster access. Just run the kubectl commands directly.
- Do NOT try to access ManifestWork, ManagedCluster, or HostedCluster CRDs — these live on the management cluster, not the service cluster. Use the controlplane scope for those.
- On the service cluster, only check: pods, logs, events in namespaces `aro-hcp`, `clusters-service`, `maestro`.
- When checking logs, always use `--tail=100` to limit output size. Do NOT ask the user for a time window.
- Use per-pod log fetching: list pods, then fetch logs from ONE pod per deployment.
- Keep output concise — summarize findings, do not include full log dumps.

## HCP Deletion Flow on Service Cluster

```
1. Frontend receives DELETE → calls CS DeleteCluster() → CS state "uninstalling"
2. Backend polls CS GetClusterStatus() until 404 (cluster fully deleted)
3. CS sends deletion via Maestro → Maestro Agent deletes ManifestWork on mgmt cluster
4. Once CS returns 404 → backend marks operation Succeeded, deletes Cosmos docs
```

## Workflow

### Step 1: Check Backend Pods
* Run: `kubectl get pods -n aro-hcp -l app=aro-hcp-backend -o wide`
* Verify pods are Running and Ready

### Step 2: Check Backend Logs for Deletion Operation
* List pods: `kubectl get pods -n aro-hcp -l app=aro-hcp-backend -o name`
* For one pod: `kubectl logs <pod> -n aro-hcp -c aro-hcp-backend --tail=100 | grep -i "<cluster-name>"`
* Look for: operation status, CS state ("uninstalling", "ready", "error"), "still deleting" messages
* If CS returns "ready" during a DELETE → cluster never entered uninstalling state (CS issue)

### Step 3: Check Cluster Service Status
* List pods: `kubectl get pods -n clusters-service -l app=clusters-service -o name`
* For one pod: `kubectl logs <pod> -n clusters-service -c clusters-service --tail=100 | grep -i "<cluster-name>"`
* Look for: "uninstalling", "deleting", errors during deletion, ManifestWork deletion status

### Step 4: Check Maestro Server
* Run: `kubectl get pods -n maestro`
* Run: `kubectl logs -n maestro deployment/maestro --tail=100 | grep -i "error\|fail\|<cluster-name>"`
* Look for: ResourceBundle deletion failures, MQTT errors, "soft-deleted" vs fully deleted bundles

### Step 5: Check for Orphaned Namespace Bundles (from logs only)
* Check Maestro and CS logs for signs of orphaned bundles:
  - Maestro logs showing repeated "resource not found" or "soft-deleted" for the cluster's bundle ID
  - CS logs showing "namespace bundle" references or repeated reconciliation for a deleted cluster
  - If the cluster's HCP namespaces keep reappearing on the mgmt cluster after deletion, the `*-00-hcp-namespaces` Maestro ResourceBundle is likely orphaned
* NOTE: ManifestWork is a management cluster resource — do NOT try to query it from the service cluster. Diagnose from Maestro/CS logs only.

### Step 6: Check Recent Events
* Run: `kubectl get events -n aro-hcp --sort-by=.lastTimestamp | tail -20`
* Run: `kubectl get events -n clusters-service --sort-by=.lastTimestamp | tail -20`

## Synthesize Findings

* If backend shows CS state "uninstalling" for a long time → stuck on mgmt cluster (escalate to controlplane)
* If backend shows CS state "ready" during DELETE → CS never started deletion (CS bug or race condition)
* If CS returns 404 but backend hasn't marked Succeeded → backend controller issue
* If Maestro has errors → ResourceBundle deletion failed (check Maestro Agent on mgmt cluster)
* If namespaces keep recreating → orphaned Maestro namespace bundle

## Recommended Remediation Steps
* CS not uninstalling: retry deletion via CS API directly
* Maestro bundle orphaned: delete orphaned ResourceBundle from Maestro
* CS stuck: check CS controller logs for specific error
* All service cluster components healthy but stuck: escalate to controlplane investigation
