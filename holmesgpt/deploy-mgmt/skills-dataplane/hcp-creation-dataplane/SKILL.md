---
name: hcp-creation-dataplane
description: Troubleshoot HCP cluster creation failures on the customer data plane cluster. Use this when worker nodes are not joining, OpenShift operators are degraded, cluster version is not converging, or pods are failing on the customer cluster.
---

## Important Instructions
- Do NOT check `kubectl config current-context` or kubeconfig availability. You are running in-cluster with a ServiceAccount — kubectl works automatically.
- Do NOT ask the user for kubeconfig, context, or cluster access. Just run the kubectl commands directly.

## Goal
Diagnose why an ARO-HCP customer cluster's data plane is not healthy after control plane creation succeeded. Check worker nodes, OpenShift operators, and cluster convergence.

## Data Plane Creation Flow

```
1. CAPI provisions Azure VMs for worker nodes
2. Nodes bootstrap via ignition and join the cluster
3. OpenShift operators deploy to worker nodes (networking, storage, registry, etc.)
4. ClusterVersion operator converges all operators to target version
5. Cluster reaches Available state with all operators healthy
```

## Workflow

### Step 1: Check Node Status
* `kubectl get nodes -o wide`
* All nodes should be Ready
* Check VERSION column matches expected OpenShift version
* If no nodes: CAPI machines haven't provisioned (investigate controlplane)

### Step 2: Check Node Conditions
* `kubectl describe nodes | grep -A5 "Conditions:"`
* Look for pressure conditions:
  - `MemoryPressure` — should be False
  - `DiskPressure` — should be False
  - `PIDPressure` — should be False
  - `NetworkUnavailable` — should be False

### Step 3: Check Node Resource Usage
* `kubectl top nodes`
* High CPU (>90%) or memory (>90%) indicates resource exhaustion

### Step 4: Check ClusterVersion
* `kubectl get clusterversion`
* Check: AVAILABLE, PROGRESSING columns
* `kubectl get clusterversion version -o yaml`
* Check `status.conditions`:
  - `Available` — should be True
  - `Progressing` — True during install, False when done
  - `Failing` — should be False (if True, check message for which operator)
* Check `status.history`:
  - Latest entry should have `state: Completed`
  - If `state: Partial`, installation is still in progress

### Step 5: Check All ClusterOperators
* `kubectl get clusteroperators`
* All operators should show: AVAILABLE=True, PROGRESSING=False, DEGRADED=False
* Identify any operators that are not Available or are Degraded

### Step 6: Check Degraded Operators
* `kubectl get co -o json | jq '.items[] | select(.status.conditions[] | select(.type=="Degraded" and .status=="True")) | {name: .metadata.name, message: (.status.conditions[] | select(.type=="Degraded") | .message)}'`
* Common failing operators during creation:
  - `console` — needs ingress to be ready first
  - `ingress` — needs DNS and load balancer
  - `monitoring` — needs persistent storage
  - `image-registry` — needs storage backend
  - `dns` — needs networking to be ready

### Step 7: Check Unavailable Operators
* `kubectl get co -o json | jq '.items[] | select(.status.conditions[] | select(.type=="Available" and .status=="False")) | {name: .metadata.name, message: (.status.conditions[] | select(.type=="Available") | .message)}'`

### Step 8: Check API Server Readiness
* `kubectl get --raw='/readyz?verbose'`
* All readiness checks should pass
* If any fail, the API server has issues

### Step 9: Check Pods Not Running
* `kubectl get pods -A --field-selector=status.phase!=Running,status.phase!=Succeeded -o wide`
* Any pods in CrashLoopBackOff, ImagePullBackOff, or Pending indicate issues
* For each failing pod: `kubectl describe pod <name> -n <namespace>`

### Step 10: Check Pods with Restarts
* `kubectl get pods -A -o custom-columns='NS:.metadata.namespace,POD:.metadata.name,RESTARTS:.status.containerStatuses[*].restartCount' --no-headers | egrep -v ' 0(,0)*$' | sort -t' ' -k3 -rn | head -20`
* High restart counts indicate recurring failures

### Step 11: Check Warning Events
* `kubectl get events -A --sort-by=.lastTimestamp --field-selector type=Warning | tail -30`
* Look for: scheduling failures, image pull errors, volume mount issues, resource quota

### Step 12: Check Networking
* `kubectl get pods -n openshift-sdn -o wide` or `kubectl get pods -n openshift-ovn-kubernetes -o wide`
* Verify network operator pods are running on all nodes
* Check for `NetworkUnavailable` node condition

### Step 13: Check Storage
* `kubectl get pvc -A`
* Any PVCs in Pending state indicate storage provisioning issues
* `kubectl get storageclass`
* Verify default storage class exists

### Step 14: Check Ingress
* `kubectl get pods -n openshift-ingress -o wide`
* `kubectl get ingresscontroller -n openshift-ingress-operator -o yaml`
* Ingress issues block console, OAuth, and route-based services

## Synthesize Findings

* **No nodes** → CAPI machines not provisioned (controlplane issue, not dataplane)
* **Nodes NotReady** → kubelet issues, networking not configured, bootstrap incomplete
* **ClusterVersion Partial** → some operators still converging (may just need time)
* **Specific operator Degraded** → investigate that operator's pods and logs
* **console not available** → usually depends on ingress; check ingress first
* **Pods in ImagePullBackOff** → registry or network connectivity issue
* **Pods in Pending** → scheduling issue (node resources, taints, affinity)
* **PVCs Pending** → storage class or Azure disk quota issue

## Recommended Remediation Steps
* Node not ready: check kubelet logs via `kubectl describe node` and machine console output
* Operator degraded: check operator-specific namespace pods and logs
* Image pull failures: verify container registry connectivity and pull secrets
* Storage issues: check Azure disk quota and storage class provisioner
* Networking issues: check OVN/SDN pods and network operator logs
* If all operators are available but ClusterVersion shows Partial: usually just needs more time for convergence (15-30 min after nodes join)
