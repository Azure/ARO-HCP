# Cleanup Procedure for HCP Clusters Stuck on Deletion

## Overview

This document provides a systematic procedure for manually cleaning up ARO HCP clusters that become stuck during deletion. Stuck deletions occur when Kubernetes controllers fail to complete cleanup operations and remove resource finalizers, causing the deletion chain to halt indefinitely.

## Prerequisites

- Access to the Management Cluster where the HCP cluster is hosted (via `hcpctl mc breakglass <mc-name>`)
- Access to the Service Cluster (via `hcpctl sc breakglass <svc-name>`)
- `kubectl` access configured with appropriate permissions
- `jq` installed locally (and `curl` if you use the provided API examples)
- Cluster ID or cluster name of the stuck cluster

## Understanding the Resource Hierarchy

ARO HCP clusters create resources across multiple layers. Understanding this hierarchy is critical for systematic troubleshooting.

### Service Cluster Layer

```
Service Cluster (SVC)
├── Clusters Service (ns: clusters-service)
│   └── Cluster record (REST API, not a CRD)
│       └── State: installing / ready / uninstalling
└── Maestro Server (ns: maestro)
    └── ResourceBundle (REST API, not a CRD)
        ├── Namespace bundle (containsNamespaces: true)
        │   └── Creates *-00-hcp-namespaces ManifestWork
        └── Main cluster bundle
            └── Creates main ManifestWork with HostedCluster, secrets, etc.
```

### Management Cluster Layer

```
Management Cluster (MGMT)
│
├── Namespace: local-cluster
│   ├── ManifestWork (created by Maestro Agent from ResourceBundle)
│   │   ├── Contains all manifests for the HCP cluster
│   │   ├── Finalizer: cluster.open-cluster-management.io/manifest-work-cleanup
│   │   └── Status feedback from applied resources
│   ├── AppliedManifestWork (tracks applied resources)
│   │   └── Created by work agent as anchor for ManifestWork resources
│   └── ManagedCluster (ACM/MCE cluster registration)
│       ├── Finalizer: cluster.open-cluster-management.io/api-resource-cleanup
│       └── References the HostedCluster
│
├── Namespace: ocm-${CLUSTER_PREFIX}-${CLUSTER_ID} (HostedCluster namespace)
│   ├── HostedCluster (Primary HCP resource)
│   │   └── Finalizer: hypershift.openshift.io/finalizer
│   ├── NodePool(s) (one per worker pool)
│   │   └── Finalizer: hypershift.openshift.io/finalizer
│   ├── Secrets (credentials, certificates, pull secrets)
│   ├── ConfigMaps (configuration data)
│   └── Other Hypershift-managed resources
│
└── Namespace: ocm-${CLUSTER_PREFIX}-${CLUSTER_ID}-${CLUSTER_NAME} (Control Plane namespace)
    ├── Deployments (capi-provider, cluster-api, control-plane-operator)
    │   └── Finalizer: hypershift.openshift.io/component-finalizer
    ├── Cluster (cluster.x-k8s.io)
    │   └── Finalizer: cluster.cluster.x-k8s.io
    ├── HostedControlPlane
    │   └── Finalizer: hypershift.openshift.io/finalizer
    ├── MachineDeployment / MachineSet / Machine (cluster.x-k8s.io)
    │   └── Finalizers: cluster.x-k8s.io/machinedeployment,
    │                   cluster.x-k8s.io/machineset,
    │                   machine.cluster.x-k8s.io
    ├── AzureMachine (infrastructure.cluster.x-k8s.io)
    │   └── Finalizer: azuremachine.infrastructure.cluster.x-k8s.io
    ├── StatefulSets (etcd, control plane components)
    ├── Services, Pods, Secrets, ConfigMaps
    └── PersistentVolumeClaims (etcd storage)
```

> **Note**: `HostedControlPlane` lives in the **Control Plane namespace** (the `ocm-${CLUSTER_PREFIX}-${CLUSTER_ID}-${CLUSTER_NAME}` namespace), not in the `HostedCluster` namespace.

### Deletion Chain Dependencies

The deletion process follows this dependency order. Blocking at any level prevents cleanup of resources above it:

```
Clusters Service (sets state to "uninstalling")
  → Maestro Server (deletes ResourceBundle)
    → Maestro Agent (deletes ManifestWork on MGMT)
      → ManifestWork cleanup (deletes HostedCluster, ManagedCluster, etc.)
        → HostedCluster deletion (finalizer: hypershift.openshift.io/finalizer)
          → NodePool deletion (finalizer: hypershift.openshift.io/finalizer)
          → Control Plane namespace cleanup
            → Deployments (finalizer: hypershift.openshift.io/component-finalizer)
            → Cluster CRD (finalizer: cluster.cluster.x-k8s.io)
            → HostedControlPlane (finalizer: hypershift.openshift.io/finalizer)
            → MachineDeployment / MachineSet / Machine (CAPI finalizers)
            → AzureMachine (finalizer: azuremachine.infrastructure.cluster.x-k8s.io)
```

**Key insight**: Deleting resources on the management cluster without first removing the source (ResourceBundle in Maestro / cluster record in CS) will result in the resources being **recreated** by the Maestro Agent.

## Systematic Troubleshooting Procedure

### Phase 1: Identify Stuck Resources (Management Cluster)

Connect to the management cluster:

```bash
hcpctl mc breakglass <mc-name>
export KUBECONFIG=<path-from-output>
```

#### Step 1: Find the Cluster Namespaces

```bash
# Find the HostedCluster prefix
kubectl get namespaces | grep ocm-
export CLUSTER_PREFIX="arohcpint"  # arohcpprod, arohcpstg,...

# Find namespaces for a specific cluster ID
export CLUSTER_ID="your-cluster-id"
kubectl get namespaces | grep ${CLUSTER_ID}
```

#### Step 2: Check Resource State

```bash
# Check HostedClusters with deletionTimestamp (stuck deleting)
kubectl get hostedcluster -A -o json | \
  jq -r '.items[] | select(.metadata.deletionTimestamp != null) |
  "\(.metadata.namespace)\t\(.metadata.name)\t\(.metadata.deletionTimestamp)"'

# Check ManifestWork stuck deleting
kubectl get manifestwork -n local-cluster -o json | \
  jq -r '.items[] | select(.metadata.deletionTimestamp != null) |
  "\(.metadata.name)\t\(.metadata.deletionTimestamp)"'

# Check for namespaces stuck in Terminating
kubectl get namespaces -o json | \
  jq -r '.items[] | select(.status.phase == "Terminating") |
  "\(.metadata.name)\t\(.metadata.deletionTimestamp)"'

# Check ManagedClusters
kubectl get managedcluster

# Check all HostedClusters and their status
kubectl get hostedcluster -A
```

#### Step 3: Identify Orphaned Resources

Look for mismatches indicating partial deletion:

```bash
# Namespaces without a HostedCluster (orphaned)
# Compare namespace list with HostedCluster list
kubectl get namespaces | grep ocm-${CLUSTER_PREFIX}
kubectl get hostedcluster -A

# ManifestWork without a corresponding cluster
kubectl get manifestwork -n local-cluster
```

#### Step 4: Investigate Why Resources Are Stuck

```bash
# Check HostedCluster conditions (look for errors)
kubectl get hostedcluster <name> -n <namespace> \
  -o jsonpath='{.status.conditions}' | jq .

# Check what's blocking a Terminating namespace
kubectl get namespace <namespace> -o json | jq '.status'

# Check Hypershift operator logs
kubectl logs -n hypershift deployment/operator --tail=50 | grep <cluster-name-or-id>

# Check resources with finalizers in a namespace (core/built-in kinds only)
kubectl get all -n <namespace> -o json | \
  jq '[.items[] | select(.metadata.finalizers != null) |
  {kind, name: .metadata.name, finalizers: .metadata.finalizers}]'

# NOTE: `kubectl get all` does NOT return many namespaced CRDs (including common blockers
# like HostedControlPlane and CAPI Cluster). Also check key Hypershift/CAPI CRDs:
kubectl get hostedclusters.hypershift.openshift.io,hostedcontrolplanes.hypershift.openshift.io,clusters.cluster.x-k8s.io \
  -n <namespace> -o json | \
  jq '[.items[] | select(.metadata.finalizers != null) |
  {kind, name: .metadata.name, finalizers: .metadata.finalizers}]'
```

### Phase 2: Check the Service Cluster

Before removing resources on the management cluster, check if the source (CS/Maestro) will recreate them.

Connect to the service cluster:

```bash
hcpctl sc breakglass <svc-name>
export KUBECONFIG=<path-from-output>
```

#### Step 5: Check Clusters Service

```bash
# List all clusters known to CS
kubectl exec -n clusters-service deployment/clusters-service -- \
  curl -s 'http://localhost:8000/api/clusters_mgmt/v1/clusters' | \
  jq '[.items[] | {id, name, state}]'

# Get details for a specific cluster
kubectl exec -n clusters-service deployment/clusters-service -- \
  curl -s "http://localhost:8000/api/clusters_mgmt/v1/clusters/${CLUSTER_ID}" | \
  jq '{id, name, state, azure: {subscription_id: .azure.subscription_id, resource_group_name: .azure.resource_group_name, resource_name: .azure.resource_name}}'
```

#### Step 6: Check Maestro Resource Bundles

```bash
# Search for resource bundles by cluster ID (search in manifest content)
kubectl exec -n maestro deployment/maestro -c maestro-server -- sh -c \
  "curl -s 'http://localhost:8000/api/maestro/v1/resource-bundles?size=2900'" | \
  jq --arg cid "${CLUSTER_ID}" '[.items[] | select(.manifests | tostring | test($cid)) |
  {id, name, created_at, consumer_name, deleted_at: .metadata.deleted_at,
   labels: .metadata.labels}]'
```

### Phase 3: Resolution

#### Strategy 1: Delete via Clusters Service API (Preferred)

If the cluster still exists in CS, trigger deletion through the proper API:

```bash
# Delete cluster via the ARO-HCP API endpoint
kubectl exec -n clusters-service deployment/clusters-service -- \
  curl -s -X DELETE \
  "http://localhost:8000/api/aro_hcp/v1alpha1/clusters/${CLUSTER_ID}"
```

> **Note**: The endpoint is `/api/aro_hcp/v1alpha1/clusters/<id>`, NOT `/api/clusters_mgmt/v1/clusters/<id>`. The latter will reject the request with "ARO-HCP related clusters operation can only be performed using ARO-HCP endpoint".

Monitor the cluster state:

```bash
kubectl exec -n clusters-service deployment/clusters-service -- \
  curl -s "http://localhost:8000/api/clusters_mgmt/v1/clusters/${CLUSTER_ID}" | \
  jq '{id, name, state}'
# Expected: state should change to "uninstalling"
```

#### Strategy 2: Delete Orphaned Maestro Resource Bundles

If the cluster is gone from CS but resource bundles remain in Maestro (causing ManifestWork recreation):

```bash
# Delete the resource bundle by ID
kubectl exec -n maestro deployment/maestro -c maestro-server -- sh -c \
  "curl -s -X DELETE 'http://localhost:8000/api/maestro/v1/resource-bundles/<bundle-id>'"

# Verify deletion
kubectl exec -n maestro deployment/maestro -c maestro-server -- sh -c \
  "curl -s 'http://localhost:8000/api/maestro/v1/resource-bundles/<bundle-id>'"
# Expected: {"code":"maestro-7","reason":"Resource with id='...' not found"}
```

> **Note**: Maestro bundles behave differently on `DELETE`:
> - **Readonly bundles** (the `*-readonly` HostedCluster / NodePool reflections) are hard-deleted immediately.
> - **Bundles backing a `ManifestWork`** are only soft-deleted — the `DELETE` returns `204`, but the bundle remains listed with `metadata.deleted_at` set. The record is only removed from the Maestro store after the corresponding `ManifestWork` on the management cluster is also removed.
>
> If you see bundles still listed with a non-null `deleted_at` after `DELETE`, that is expected — proceed to Strategy 3 to clear the `ManifestWork` finalizers so Maestro can finish its own cleanup.

#### Strategy 3: Manual Finalizer Removal on Management Cluster (Last Resort)

Only after ensuring the source (CS/Maestro) won't recreate resources, remove finalizers **bottom-up** on the management cluster:

```bash
# 1. Check what's blocking the CP namespace (if Terminating):
kubectl get namespace <cp-namespace> -o json | jq '.status.conditions[] | select(.type == "NamespaceContentRemaining")'

# 2. Remove finalizers from CP namespace resources (bottom-up, leaf resources first):
# Deployments with hypershift.openshift.io/component-finalizer
kubectl patch deployment <name> -n <cp-namespace> \
  --type=json -p='[{"op": "replace", "path": "/metadata/finalizers", "value": []}]'

# AzureMachines (infrastructure.cluster.x-k8s.io)
# Uses a merge patch so the command is idempotent even if .metadata.finalizers is absent
# on some items (JSONPatch `replace` would fail on those).
for name in $(kubectl get azuremachines.infrastructure.cluster.x-k8s.io -n <cp-namespace> -o jsonpath='{.items[*].metadata.name}'); do
  kubectl patch azuremachines.infrastructure.cluster.x-k8s.io $name -n <cp-namespace> \
    --type=merge -p='{"metadata":{"finalizers":null}}'
done

# Machines / MachineSets / MachineDeployments (cluster.x-k8s.io)
for kind in machines.cluster.x-k8s.io machinesets.cluster.x-k8s.io machinedeployments.cluster.x-k8s.io; do
  for name in $(kubectl get $kind -n <cp-namespace> -o jsonpath='{.items[*].metadata.name}'); do
    kubectl patch $kind $name -n <cp-namespace> \
      --type=merge -p='{"metadata":{"finalizers":null}}'
  done
done

# Cluster CRD with cluster.cluster.x-k8s.io finalizer
kubectl patch clusters.cluster.x-k8s.io <name> -n <cp-namespace> \
  --type=json -p='[{"op": "replace", "path": "/metadata/finalizers", "value": []}]'

# HostedControlPlane with hypershift.openshift.io/finalizer
kubectl patch hostedcontrolplanes.hypershift.openshift.io <name> -n <cp-namespace> \
  --type=json -p='[{"op": "replace", "path": "/metadata/finalizers", "value": []}]'

# 3. Remove NodePool finalizers in the HostedCluster namespace
# Uses a merge patch (idempotent when .metadata.finalizers is absent).
for name in $(kubectl get nodepool -n ocm-${CLUSTER_PREFIX}-${CLUSTER_ID} -o jsonpath='{.items[*].metadata.name}'); do
  kubectl patch nodepool $name -n ocm-${CLUSTER_PREFIX}-${CLUSTER_ID} \
    --type=merge -p='{"metadata":{"finalizers":null}}'
done

# 4. Remove HostedCluster finalizer (only after CP namespace resources and NodePools are cleared)
kubectl patch hostedcluster <name> -n ocm-${CLUSTER_PREFIX}-${CLUSTER_ID} \
  --type=json -p='[{"op": "replace", "path": "/metadata/finalizers", "value": []}]'

# 5. Remove ManifestWork finalizers
kubectl patch manifestwork <name> -n local-cluster \
  --type=json -p='[{"op": "replace", "path": "/metadata/finalizers", "value": []}]'

# 6. Delete orphaned namespaces (only after all resources are cleared)
kubectl delete namespace <namespace>
```

### Phase 4: Verify Cleanup

```bash
# On the Management Cluster
kubectl get namespaces | grep ${CLUSTER_ID}
kubectl get manifestwork -n local-cluster | grep ${CLUSTER_ID}
kubectl get managedcluster | grep ${CLUSTER_ID}
kubectl get hostedcluster -A | grep ${CLUSTER_ID}
kubectl get appliedmanifestwork | grep ${CLUSTER_ID}

# On the Service Cluster - verify CS no longer has the cluster
kubectl exec -n clusters-service deployment/clusters-service -- \
  curl -s 'http://localhost:8000/api/clusters_mgmt/v1/clusters' | \
  jq '[.items[] | {id, name, state}]'

# On the Service Cluster - verify no orphaned Maestro bundles
kubectl exec -n maestro deployment/maestro -c maestro-server -- sh -c \
  "curl -s 'http://localhost:8000/api/maestro/v1/resource-bundles?size=2900'" | \
  jq --arg cid "${CLUSTER_ID}" '[.items[] | select(.manifests | tostring | test($cid)) | {id, name}]'
```

> **Note**: If CS still has the cluster record in `uninstalling` state after management cluster cleanup, re-issue the ARO-HCP `DELETE` (Strategy 1). With Maestro and the management cluster cleared, the CS controller can now complete the deletion and subsequent `GET`s will return `404`.

## Common Stuck Scenarios

### Scenario 1: HostedCluster Stuck Deleting - Azure Resources Already Gone

**Symptoms**: Hypershift operator logs show "hostedcluster is still deleting" in a tight loop every ~5 seconds. HostedCluster conditions show `ResourceGroupNotFound` or similar Azure errors.

**Cause**: The Azure resource group was deleted externally, so the Hypershift operator can't clean up cloud resources and won't remove its finalizer.

**Fix**: Remove the `hypershift.openshift.io/finalizer` from the HostedCluster. This is safe because the Azure resources are already gone.

### Scenario 2: ManifestWork/Namespaces Keep Being Recreated

**Symptoms**: You delete ManifestWork or namespaces on the management cluster, but they reappear within seconds.

**Cause**: The Maestro ResourceBundle still exists on the service cluster. The Maestro Agent continuously reconciles and recreates the ManifestWork.

**Fix**: Delete the source first — either trigger deletion via CS API (Strategy 1) or delete the ResourceBundle from Maestro directly (Strategy 2). Only then clean up management cluster resources.

### Scenario 3: Orphaned Namespace Resource Bundles

**Symptoms**: Cluster is gone from CS, main ManifestWork is gone, but `*-00-hcp-namespaces` ManifestWork keeps being recreated along with the CP namespace.

**Cause**: CS deleted the main cluster resource bundles from Maestro but left behind the namespace-only bundle (`containsNamespaces: true` label). This is a known issue in the CS deletion flow.

**Fix**: Find and delete the orphaned resource bundle from Maestro (Strategy 2). The bundle can be identified by its `api.openshift.com/id` label matching the cluster ID.

### Scenario 4: CP Namespace Stuck Terminating

**Symptoms**: `kubectl get namespace <cp-namespace>` shows `Terminating` but never completes.

**Cause**: Resources with finalizers remain in the namespace. Common blockers:
- `clusters.cluster.x-k8s.io` with `cluster.cluster.x-k8s.io` finalizer
- `hostedcontrolplanes.hypershift.openshift.io` with `hypershift.openshift.io/finalizer`
- `deployments` with `hypershift.openshift.io/component-finalizer`

**Fix**: Check `kubectl get namespace <ns> -o json | jq '.status.conditions'` to identify blocking resources, then remove their finalizers (Strategy 3).

### Scenario 5: CP Namespace Stuck After Cluster and HostedControlPlane Are Cleared

**Symptoms**: After force-removing finalizers on the CAPI `Cluster`, the `HostedControlPlane`, and the Hypershift Deployments, the CP namespace is still `Terminating`. `kubectl get namespace <cp-ns> -o json | jq '.status.conditions'` references infrastructure or machine kinds.

**Cause**: CAPI machine resources hold their own finalizers and are not removed when the parent `Cluster` is force-deleted (finalizer cleared). Common blockers in the CP namespace:

- `azuremachines.infrastructure.cluster.x-k8s.io` (`azuremachine.infrastructure.cluster.x-k8s.io`)
- `machines.cluster.x-k8s.io` (`machine.cluster.x-k8s.io`)
- `machinesets.cluster.x-k8s.io` (`cluster.x-k8s.io/machineset`)
- `machinedeployments.cluster.x-k8s.io` (`cluster.x-k8s.io/machinedeployment`)

**Fix**: Patch the finalizers on all four kinds in the CP namespace (see Strategy 3, step 2).

## Quick Reference: Key API Endpoints

| Component | Endpoint | Notes |
|---|---|---|
| CS - List clusters | `http://localhost:8000/api/clusters_mgmt/v1/clusters` | Via exec into CS pod |
| CS - Delete cluster | `http://localhost:8000/api/aro_hcp/v1alpha1/clusters/<id>` | Must use `aro_hcp` endpoint |
| Maestro - List bundles | `http://localhost:8000/api/maestro/v1/resource-bundles` | Via exec into maestro-server container |
| Maestro - Delete bundle | `http://localhost:8000/api/maestro/v1/resource-bundles/<id>` | DELETE method |

## Quick Reference: Common Finalizers

| Finalizer | Resource | Controller |
|---|---|---|
| `hypershift.openshift.io/finalizer` | HostedCluster, HostedControlPlane | Hypershift Operator |
| `hypershift.openshift.io/component-finalizer` | Deployments in CP namespace | Hypershift Operator |
| `cluster.cluster.x-k8s.io` | Cluster (CAPI) | Cluster API |
| `machine.cluster.x-k8s.io` | Machine (CAPI) | Cluster API |
| `cluster.x-k8s.io/machineset` | MachineSet (CAPI) | Cluster API |
| `cluster.x-k8s.io/machinedeployment` | MachineDeployment (CAPI) | Cluster API |
| `azuremachine.infrastructure.cluster.x-k8s.io` | AzureMachine | CAPZ (Cluster API Provider Azure) |
| `cluster.open-cluster-management.io/manifest-work-cleanup` | ManifestWork | ACM Work Agent |
| `cluster.open-cluster-management.io/api-resource-cleanup` | ManagedCluster | ACM |

## Quick Reference: Key Controller Logs

```bash
# Hypershift operator (manages HostedCluster deletion)
kubectl logs -n hypershift deployment/operator --tail=100 | grep <cluster-name>

# Maestro Agent (manages ManifestWork lifecycle)
kubectl logs -n maestro deployment/maestro-agent -c maestro-agent | grep <cluster-id>

# Work agent (manages ManifestWork application)
kubectl logs -n open-cluster-management-agent deployment/klusterlet-work-agent
```
