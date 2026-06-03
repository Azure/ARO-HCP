---
name: hcp-creation-serviceplane
description: Troubleshoot HCP cluster creation failures on the service cluster. Use this when a cluster is stuck in Accepted or Provisioning state, or when the RP frontend/backend, Cluster Service, or Maestro Server have issues during cluster creation.
---

## Goal
Diagnose why an ARO-HCP cluster creation is failing or stuck by investigating the service cluster components involved in the creation workflow.

## Important Instructions
- Do NOT check `kubectl config current-context` or kubeconfig availability. You are running in-cluster with a ServiceAccount — kubectl works automatically.
- Do NOT ask the user for kubeconfig, context, or cluster access. Just run the kubectl commands directly.
- Do NOT try to access ManifestWork, ManagedCluster, HostedCluster, or HyperShift CRDs — these live on the management cluster, not here. Only check pods, logs, events in `aro-hcp`, `clusters-service`, `maestro` namespaces.
- When checking logs, always use `--tail=100` to limit output size. Do NOT ask the user for a time window.
- When grepping for a cluster name, use the cluster name from the user's question.
- Execute all steps — do not skip log checks. If a grep returns no results, report that and move on.
- Use per-pod log fetching: first list pods, then fetch logs from ONE pod per deployment (not all replicas).
- Keep output concise — summarize log findings, do not include full log dumps in your response.

## HCP Creation Flow on Service Cluster

```
1. Frontend receives PUT → validates → creates in CosmosDB → calls Cluster Service
2. Cluster Service creates cluster record → provisions managed resource group → creates ManifestWork
3. Backend polls operation status → checks CS state → checks ManifestWork/HostedCluster via Maestro
4. Maestro Server routes ManifestWork to Maestro Agent on management cluster
```

## Workflow

### Step 1: Check Frontend Pods
* Run: `kubectl get pods -n aro-hcp -l app=aro-hcp-frontend -o wide`
* Verify all pods are Running and Ready (2/2)
* If not running: `kubectl describe pods -n aro-hcp -l app=aro-hcp-frontend`

### Step 2: Check Frontend Logs
* List frontend pods: `kubectl get pods -n aro-hcp -l app=aro-hcp-frontend -o name`
* For each pod, run: `kubectl logs <pod-name> -n aro-hcp -c aro-hcp-frontend --tail=100 | grep -i "<cluster-name>"`
* Look for: HTTP status codes on the PUT request, validation errors, Cluster Service call failures
* Common issues: 400 (validation), 409 (conflict), 500 (CS unreachable)

### Step 3: Check Backend Pods
* Run: `kubectl get pods -n aro-hcp -l app=aro-hcp-backend -o wide`
* Verify all pods are Running and Ready (2/2)

### Step 4: Check Backend Logs
* List backend pods: `kubectl get pods -n aro-hcp -l app=aro-hcp-backend -o name`
* For each pod, run: `kubectl logs <pod-name> -n aro-hcp -c aro-hcp-backend --tail=100 | grep -i "<cluster-name>"`
* Look for operation state transitions (Accepted → Provisioning → Succeeded/Failed)
* Key error messages to search for:
  - `"maestro bundle is degraded"` — ManifestWork has issues on management cluster
  - `"hosted cluster is not available"` — HostedCluster control plane not ready
  - `"hosted cluster has no installed version"` — ClusterVersion not completed
  - `"hosted cluster has no control plane endpoint"` — kube-apiserver not ready

### Step 5: Check Cluster Service Pods
* Run: `kubectl get pods -n clusters-service`
* Verify `clusters-service` pods are Running and Ready

### Step 6: Check Cluster Service Logs
* List CS pods: `kubectl get pods -n clusters-service -l app=clusters-service -o name`
* For one pod, run: `kubectl logs <pod-name> -n clusters-service -c clusters-service --tail=100 | grep -i "<cluster-name>"`
* Look for: cluster creation events, ManifestWork creation status, Azure resource provisioning errors

### Step 7: Check Cluster Service Database
* Run: `kubectl get pods -n clusters-service -l app=ocm-cs-db`
* Verify the CS database pod is Running and Ready

### Step 8: Check Maestro Server
* Run: `kubectl get pods -n maestro`
* Run: `kubectl logs -n maestro deployment/maestro --tail=200 | grep -i "error\|fail\|timeout"`
* Look for: ManifestWork routing failures, MQTT/Event Grid connectivity issues

### Step 9: Check Recent Events
* Run: `kubectl get events -n aro-hcp --sort-by=.lastTimestamp | tail -20`
* Run: `kubectl get events -n clusters-service --sort-by=.lastTimestamp | tail -20`
* Run: `kubectl get events -n maestro --sort-by=.lastTimestamp | tail -20`

## Synthesize Findings

* If frontend returned an error → the request never reached Cluster Service (validation or auth issue)
* If CS logs show errors → the cluster record or Azure resources failed to create
* If backend shows "maestro bundle is degraded" → ManifestWork wasn't applied on the management cluster (escalate to controlplane investigation)
* If backend shows "hosted cluster is not available" → HostedCluster exists but control plane isn't ready (escalate to controlplane investigation)
* If Maestro Server has errors → MQTT/Event Grid connectivity issue
* If all service cluster components are healthy and cluster shows Succeeded → the issue is on the controlplane or dataplane

## Recommended Remediation Steps
* Frontend validation errors: fix the request payload
* CS provisioning failures: check Azure quotas, network policies, managed identity permissions
* Maestro connectivity: check Event Grid Namespace health, verify MQTT credentials
* If service cluster is healthy but cluster is stuck: escalate to controlplane investigation using scope=controlplane
