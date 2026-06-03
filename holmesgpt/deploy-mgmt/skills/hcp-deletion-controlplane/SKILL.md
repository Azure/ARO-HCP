---
name: hcp-deletion-controlplane
description: Troubleshoot HCP cluster deletion stuck on the management cluster. Use this when HostedCluster has stuck finalizers, namespace is stuck Terminating, CAPI machines are not being cleaned up, ManagedCluster is stuck Detaching, or ManifestWork cleanup is blocked.
---

## Goal
Diagnose why an ARO-HCP cluster deletion is stuck on the management cluster by checking finalizers, namespace status, and resource cleanup across the deletion chain.

## Important Instructions
- Do NOT check `kubectl config current-context` or kubeconfig availability. You are running in-cluster with a ServiceAccount — kubectl works automatically.
- Do NOT ask the user for kubeconfig, context, or cluster access. Just run the kubectl commands directly.
- When checking logs, always use `--tail=100`. Do NOT ask the user for a time window.
- Use per-pod log fetching from ONE pod per deployment.
- Check finalizers on EVERY resource type listed — stuck finalizers are the #1 cause of stuck deletions.
- Keep output concise — summarize findings.

## Key Namespaces
* `local-cluster` — ManifestWork, ManagedCluster
* `ocm-*-{clusterID}` — HostedCluster, NodePools, secrets
* `ocm-*-{clusterID}-{clusterName}` — Control plane pods, HostedControlPlane, CAPI resources

## Deletion Chain (blocking at any level prevents cleanup above it)

```
ManifestWork cleanup → ManagedCluster destructor → ManagedClusterAddon hooks
→ HostedCluster finalizer → NodePool finalizer → CP namespace cleanup:
  Deployments (component-finalizer) → Cluster CAPI (cluster.cluster.x-k8s.io)
  → HostedControlPlane → MachineDeployment/MachineSet/Machine → AzureMachine
```

## Workflow

### Step 1: Check Maestro Agent
* Run: `kubectl get pods -n maestro -l app=maestro-agent`
* Run: `kubectl logs -n maestro -l app=maestro-agent --tail=50 | grep -i "error\|fail\|delete"`
* If agent is down, ManifestWork will never be deleted

### Step 2: Check ManifestWork in local-cluster
* Run: `kubectl get manifestwork -n local-cluster | grep <cluster-name-or-id>`
* If ManifestWork still exists: `kubectl get manifestwork <name> -n local-cluster -o jsonpath='{.metadata.finalizers}'`
* Check: `cluster.open-cluster-management.io/manifest-work-cleanup` finalizer
* If ManifestWork has this finalizer, the work agent hasn't finished cleaning up applied resources

### Step 3: Check ManagedCluster Status
* Run: `kubectl get managedcluster | grep <cluster-name-or-id>`
* Check status: is it `Detaching`?
* Run: `kubectl get managedcluster <name> -o jsonpath='{.metadata.finalizers}'`
* Look for: `cluster.open-cluster-management.io/api-resource-cleanup`

### Step 4: Check ManagedClusterAddon Finalizers (Common Blocker)
* Run: `kubectl get managedclusteraddon -n <managedcluster-name>`
* For each addon: `kubectl get managedclusteraddon <addon> -n <mc-name> -o jsonpath='{.metadata.finalizers}'`
* Look for stuck finalizers:
  - `hosting-addon-pre-delete` — pre-delete hook failed (klusterlet lost auth)
  - `hosting-manifests-cleanup` — manifests not cleaned up
* This is a common stuck point — the fix is to remove these finalizers manually

### Step 5: Check HostedCluster Namespace
* Run: `kubectl get ns | grep ocm-.*<cluster-name-or-id>`
* If namespace is `Terminating`: `kubectl get ns <ns> -o json | jq '.status.conditions'`
* Check what's blocking: `FinalizersRemaining` or `ContentDeletingResources`

### Step 6: Check HostedCluster CR and Finalizers
* Run: `kubectl get hostedcluster -A | grep <cluster-name>`
* Run: `kubectl get hostedcluster <name> -n <ns> -o jsonpath='{.metadata.finalizers}'`
* Look for: `hypershift.openshift.io/finalizer`
* If present and cluster is deleting: HyperShift operator can't remove it (check operator logs)

### Step 7: Check NodePool CRs and Finalizers
* Run: `kubectl get nodepools -A | grep <cluster-name>`
* For each: `kubectl get nodepool <name> -n <ns> -o jsonpath='{.metadata.finalizers}'`
* Look for: `hypershift.openshift.io/finalizer`

### Step 8: Check Control Plane Namespace
* Run: `kubectl get ns | grep ocm-.*<cluster-name>` (the `-{cluster-name}` suffixed namespace)
* If `Terminating`: `kubectl get ns <cp-ns> -o json | jq '.status.conditions'`
* Check what resources are blocking deletion

### Step 9: Check Resources Blocking CP Namespace Deletion
Check each resource type for stuck finalizers:
* Run: `kubectl get deployments -n <cp-ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.finalizers}{"\n"}{end}'`
  - Look for: `hypershift.openshift.io/component-finalizer`
* Run: `kubectl get cluster.cluster.x-k8s.io -n <cp-ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.finalizers}{"\n"}{end}'`
  - Look for: `cluster.cluster.x-k8s.io`
* Run: `kubectl get hostedcontrolplane -n <cp-ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.finalizers}{"\n"}{end}'`
  - Look for: `hypershift.openshift.io/finalizer`
* Run: `kubectl get machinedeployment.cluster.x-k8s.io -n <cp-ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.finalizers}{"\n"}{end}'`
* Run: `kubectl get machineset.cluster.x-k8s.io -n <cp-ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.finalizers}{"\n"}{end}'`
* Run: `kubectl get machine.cluster.x-k8s.io -n <cp-ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.finalizers}{"\n"}{end}'`
* Run: `kubectl get azuremachine -n <cp-ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.finalizers}{"\n"}{end}'` 2>/dev/null

### Step 10: Check HyperShift Operator Logs
* Run: `kubectl logs -n hypershift -l app=operator --tail=100 | grep -i "<cluster-name>\|still deleting\|error"`
* Look for: "hostedcluster is still deleting" every ~5s (means Azure RG gone but finalizer stuck)

### Step 11: Check Events
* Run: `kubectl get events -n <hc-ns> --sort-by=.lastTimestamp | tail -20`
* Run: `kubectl get events -n <cp-ns> --sort-by=.lastTimestamp | tail -20`

## Synthesize Findings

* **ManifestWork still exists with cleanup finalizer** → resources not fully deleted yet, check HostedCluster
* **ManagedClusterAddon has stuck finalizers** → klusterlet auth issue, remove addon finalizers manually
* **HostedCluster has finalizer, HyperShift says "still deleting"** → Azure RG already gone, remove finalizer
* **CP namespace Terminating, CAPI resources have finalizers** → remove finalizers on Machine/AzureMachine/MachineSet/MachineDeployment individually (removing parent Cluster finalizer doesn't cascade)
* **Namespace keeps recreating after deletion** → orphaned Maestro ResourceBundle (investigate serviceplane)
* **All finalizers gone but namespace still Terminating** → check namespace status conditions for remaining content

## Recommended Remediation Steps
* ManagedClusterAddon finalizers: `kubectl patch managedclusteraddon <addon> -n <mc> --type=merge -p '{"metadata":{"finalizers":null}}'`
* HostedCluster finalizer: `kubectl patch hostedcluster <name> -n <ns> --type=merge -p '{"metadata":{"finalizers":null}}'`
* CAPI Machine finalizers: patch each of AzureMachine, Machine, MachineSet, MachineDeployment separately
* CP namespace Deployments: `kubectl patch deployment <name> -n <cp-ns> --type=merge -p '{"metadata":{"finalizers":null}}'`
* Orphaned Maestro bundle: delete from Maestro API or CS directly
