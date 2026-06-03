---
name: hcp-creation-controlplane
description: Troubleshoot HCP cluster creation failures on the management cluster. Use this when HostedCluster is not becoming available, control plane pods are failing, NodePool machines are not provisioning, or HyperShift operator has issues.
---

## Goal
Diagnose why an ARO-HCP cluster creation is failing on the management cluster by checking HyperShift custom resources, control plane pods, and CAPI machines.

## HCP Creation Flow on Management Cluster

```
1. Maestro Agent receives ManifestWork → applies HostedCluster CR to ocm-{env}-{clusterID} namespace
2. HyperShift Operator watches HostedCluster → creates control plane namespace ocm-{env}-{clusterID}-{name}
3. Control plane pods start: etcd, kube-apiserver, openshift-apiserver, control-plane-operator
4. NodePool CR → CAPI creates Machines → AzureMachines → worker VMs provisioned
5. HostedCluster conditions converge: Available=True, Degraded=False
```

## Key Namespaces
* `ocm-*-{clusterID}` — HostedCluster CR, secrets, ManagedCluster
* `ocm-*-{clusterID}-{clusterName}` — Control plane pods (etcd, kube-apiserver, etc.)
* `maestro` — Maestro Agent
* `hypershift` — HyperShift Operator

## Workflow

### Step 1: Check Maestro Agent
* `kubectl get pods -n maestro`
* `kubectl logs -n maestro -l app=maestro-agent --tail=100 | grep -i error`
* If agent is not running or has errors, ManifestWork won't be applied

### Step 2: List HostedClusters
* `kubectl get hostedclusters -A -o wide`
* Verify the target cluster exists
* Check columns: PROGRESS, AVAILABLE, PROGRESSING, MESSAGE

### Step 3: Check HostedCluster Conditions (Critical)
* `kubectl get hostedcluster <name> -n <namespace> -o yaml`
* Check these conditions:
  - `Available` — must be True for creation to succeed
  - `Degraded` — must be False
  - `ValidHostedControlPlaneConfiguration` — must be True
  - `ValidAzureKMSConfig` — if False, etcd encryption key vault has issues (common: 403 ForbiddenByConnection)
  - `EtcdAvailable` — must be True
  - `InfrastructureReady` — must be True
  - `ExternalDNSReachable` — must be True
  - `ClusterVersionProgressing` — True during installation, False when done
  - `ClusterVersionSucceeding` — False means operators are failing
  - `ClusterVersionFailing` — shows which operator is blocking

### Step 4: Check HCP Namespace Exists
* `kubectl get ns | grep ocm-`
* There should be TWO namespaces per cluster:
  - `ocm-{env}-{clusterID}` — HostedCluster CR namespace
  - `ocm-{env}-{clusterID}-{clusterName}` — control plane pods namespace
* If only the first exists, HyperShift hasn't created the control plane yet

### Step 5: Check HostedControlPlane
* `kubectl get hostedcontrolplane -A`
* `kubectl get hostedcontrolplane <name> -n <hcp-namespace> -o yaml`
* Check conditions:
  - `Ready` — must be True
  - `Available` — must be True
  - `EtcdAvailable` — must be True
  - `KubeAPIServerAvailable` — must be True
  - `ValidAzureKMSConfig` — check for Key Vault errors

### Step 6: Check Control Plane Pods
* `kubectl get pods -n <hcp-namespace> -o wide`
* `kubectl get pods -n <hcp-namespace> --field-selector=status.phase!=Running,status.phase!=Succeeded`
* Key pods to verify:
  - `etcd-0`, `etcd-1`, `etcd-2` — must be Running with all containers Ready
  - `kube-apiserver-*` — must be Running (typically 3 replicas)
  - `openshift-apiserver-*` — must be Running
  - `control-plane-operator-*` — must be Running
  - `cluster-version-operator-*` — must be Running

### Step 7: Check etcd Health
* `kubectl get pods -n <hcp-namespace> -l app=etcd`
* `kubectl logs -n <hcp-namespace> etcd-0 -c etcd --tail=50 | grep -i "error\|fail\|unhealthy"`
* Common issues: disk pressure, PV not bound, slow disk

### Step 8: Check kube-apiserver
* `kubectl logs -n <hcp-namespace> -l app=kube-apiserver -c kube-apiserver --tail=50 | grep -i error`
* Common issues: certificate errors, etcd connection failures, KMS key vault unreachable

### Step 9: Check NodePool
* `kubectl get nodepools -A`
* `kubectl get nodepool <name> -n <namespace> -o yaml`
* Check: replicas, availableReplicas, conditions
* If replicas don't match availableReplicas, machines are not provisioning

### Step 10: Check CAPI Machines
* `kubectl get machines.cluster.x-k8s.io -A`
* `kubectl get machinedeployments.cluster.x-k8s.io -A`
* Check: phase should be Running, not Provisioning or Failed
* If machines are stuck in Provisioning: check Azure quota, subnet capacity, VM SKU availability

### Step 11: Check HyperShift Operator
* `kubectl get pods -n hypershift`
* `kubectl logs -n hypershift -l app=operator --tail=100 | grep -i error`
* If operator is not running, no HostedClusters will be created

### Step 12: Check Events in HCP Namespace
* `kubectl get events -n <hcp-namespace> --sort-by=.lastTimestamp | tail -30`
* Look for: Warning events, failed scheduling, image pull errors, resource quota

### Step 13: Check ClusterVersion Progress
* From HostedCluster status, check `status.version.history`:
  - Must have at least one entry with `state: Completed`
  - If `state: Partial`, operators are still converging
  - Check which operators are blocking in `ClusterVersionFailing` condition

## Synthesize Findings

* **No HostedCluster CR** → Maestro Agent didn't apply ManifestWork (check Maestro)
* **HostedCluster exists but no HCP namespace** → HyperShift Operator issue
* **Control plane pods crashing** → check pod logs, likely cert/auth/KMS issues
* **ValidAzureKMSConfig=False** → Key Vault connectivity (private endpoint, firewall rules)
* **etcd not available** → disk issues, PV problems, or etcd member health
* **NodePool replicas mismatch** → CAPI/Azure provisioning failure (quota, networking)
* **ClusterVersion stuck** → specific operator not available (check which one in conditions)

## Recommended Remediation Steps
* KMS 403: enable private endpoint or trusted service access on Key Vault
* etcd disk pressure: increase PV size or storage class IOPS
* Machine provisioning stuck: check Azure VM quota and subnet IP availability
* Operator not available: investigate specific operator pods in the customer cluster (dataplane scope)
