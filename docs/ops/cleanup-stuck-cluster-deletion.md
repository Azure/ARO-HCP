# Cleanup Procedure for HCP Clusters Stuck on Deletion

## Overview

This document provides a systematic procedure for manually cleaning up ARO HCP clusters that become stuck during deletion. Stuck deletions occur when Kubernetes controllers fail to complete cleanup operations and remove resource finalizers, causing the deletion chain to halt indefinitely.

## Prerequisites

- Access to the Management Cluster where the HCP cluster is hosted
- kubectl access configured with appropriate permissions
- Cluster ID or cluster name of the stuck cluster
- Understanding that manual finalizer removal should be a last resort

## Understanding the Resource Hierarchy

ARO HCP clusters create resources across multiple layers. Understanding this hierarchy is critical for systematic troubleshooting.

### Service Cluster Layer

```
Service Cluster (SVC)
└── Maestro Server
    └── ResourceBundle
        └── Represents work to be applied to Management Cluster
```

### Management Cluster Layer

```
Management Cluster (MGMT)
│
├── Namespace: local-cluster
│   ├── ManifestWork (created by Maestro Agent)
│   │   ├── Contains all manifests for the HCP cluster
│   │   ├── Finalizer: cluster.open-cluster-management.io/manifest-work-cleanup
│   │   └── Status feedback from applied resources
│   ├── AppliedManifestWork (tracks applied resources)
│   │   └── Created by work agent as anchor for ManifestWork resources
│   └── ManagedCluster (ACM/MCE cluster registration)
│       ├── Finalizer: cluster.open-cluster-management.io/api-resource-cleanup
│       └── References the HostedCluster
│
├── Namespace: ocm-xxx-${CLUSTER_ID} (HostedCluster namespace)
│   ├── HostedCluster (Primary HCP resource)
│   │   └── Finalizer: hypershift.openshift.io/finalizer
│   ├── HostedControlPlane (Control plane configuration)
│   ├── Secrets (credentials, certificates, pull secrets)
│   ├── ConfigMaps (configuration data)
│   └── Other Hypershift-managed resources
│
└── Namespace: ocm-xxx-${CLUSTER_ID}-${CLUSTER_NAME} (Control Plane namespace)
    ├── Deployments (kube-apiserver, etcd, controllers)
    ├── StatefulSets (etcd, control plane components)
    ├── Services (API server endpoints, internal services)
    ├── Pods (running control plane workloads)
    ├── Secrets (service account tokens, certificates)
    ├── ConfigMaps (component configurations)
    └── PersistentVolumeClaims (etcd storage, if applicable)
```

**Note**: The `ocm-xxx-${CLUSTER_ID}` pattern is the standard namespace naming for HostedCluster resources, where `xxx` is a fixed prefix and `CLUSTER_ID` is your cluster's unique identifier.

### Deletion Chain Dependencies

The deletion process follows this dependency order to prevent resources from being stuck in a terminating state:

1. **ManifestWork** (orchestrates deletion of all HCP resources)
   - Protected by finalizer: `cluster.open-cluster-management.io/manifest-work-cleanup`
   - Waits for AppliedManifestWork to be cleaned up
   
2. **ManagedCluster** (depends on HostedCluster and associated resources)
   - Protected by finalizer: `cluster.open-cluster-management.io/api-resource-cleanup`
   - Cleanup includes: managedClusterAddons, manifestWorks, roleBindings
   
3. **HostedCluster** (depends on control plane namespace cleanup)
   - Protected by finalizer: `hypershift.openshift.io/finalizer`
   - Must clean up Azure resources and control plane components
   
4. **Control Plane Namespace** (depends on all child resources)
   - Cannot terminate until all pods, deployments, PVCs are gone
   
5. **HostedCluster Namespace** (depends on HostedCluster deletion)
   - Cannot terminate until HostedCluster and supporting resources are removed

## Systematic Troubleshooting Procedure

### Phase 1: Identify Stuck Resources

#### Step 1: Find the Cluster Namespaces

First, identify the cluster's namespaces on the management cluster:

```bash
# Find the HostedCluster namespace
kubectl get namespaces | grep ocm-xxx-

# Or if you know the cluster ID
CLUSTER_ID="your-cluster-id"
kubectl get namespace ocm-xxx-${CLUSTER_ID}

# Find the control plane namespace
CLUSTER_NAME="your-cluster-name"
kubectl get namespace ocm-xxx-${CLUSTER_ID}-${CLUSTER_NAME}
```

#### Step 2: Check Top-Level Resources for Deletion Timestamps

Start at the top of the hierarchy and work your way down:

```bash
# Check ManifestWork in local-cluster namespace
kubectl get manifestwork -n local-cluster | grep ${CLUSTER_ID}

# Check ManifestWork status and finalizers
kubectl get manifestwork <manifestwork-name> -n local-cluster -o yaml

# Check specific finalizer
kubectl get manifestwork <manifestwork-name> -n local-cluster \
  -o jsonpath='{.metadata.finalizers}' | grep manifest-work-cleanup

# Check ManifestWork status conditions
kubectl get manifestwork <manifestwork-name> -n local-cluster \
  -o jsonpath='{.status.conditions}' | jq .

# Check resource status feedback from management cluster
kubectl get manifestwork <manifestwork-name> -n local-cluster \
  -o jsonpath='{.status.resourceStatus}' | jq .

# Check AppliedManifestWork (tracks what was applied)
kubectl get appliedmanifestwork <manifestwork-name>

# Check ManagedCluster
kubectl get managedcluster | grep ${CLUSTER_ID}
kubectl describe managedcluster <managedcluster-name>

# Check ManagedCluster finalizers
kubectl get managedcluster <managedcluster-name> \
  -o jsonpath='{.metadata.finalizers}'

# Check HostedCluster
kubectl get hostedcluster -n ocm-xxx-${CLUSTER_ID}
kubectl describe hostedcluster <cluster-name> -n ocm-xxx-${CLUSTER_ID}

# Check HostedControlPlane
kubectl get hostedcontrolplane -n ocm-xxx-${CLUSTER_ID}
kubectl describe hostedcontrolplane <cluster-name> -n ocm-xxx-${CLUSTER_ID}
```

#### Step 3: Identify Resources with DeletionTimestamp

Look for resources that have a `deletionTimestamp` but are not completing deletion:

```bash
# In the HostedCluster namespace
kubectl get all,secrets,configmaps -n ocm-xxx-${CLUSTER_ID} -o json | \
  kubectl get -o json | \
  jq '.items[] | select(.metadata.deletionTimestamp != null) | {kind: .kind, name: .metadata.name, deletionTimestamp: .metadata.deletionTimestamp, finalizers: .metadata.finalizers}'

# In the control plane namespace
kubectl get all,secrets,configmaps,pvc -n ocm-xxx-${CLUSTER_ID}-${CLUSTER_NAME} -o json | \
  jq '.items[] | select(.metadata.deletionTimestamp != null) | {kind: .kind, name: .metadata.name, deletionTimestamp: .metadata.deletionTimestamp, finalizers: .metadata.finalizers}'
```

**Note**: If `jq` is not available on your SAW device, check resources individually:

```bash
kubectl get hostedcluster <cluster-name> -n ocm-xxx-${CLUSTER_ID} -o jsonpath='{.metadata.deletionTimestamp}{"\n"}{.metadata.finalizers}'
```

### Phase 2: Follow the Deletion Chain

#### Step 4: Determine the Blocking Resource

Work from the top of the hierarchy down:

1. **If ManifestWork has deletionTimestamp:**
   - Check its finalizer: `cluster.open-cluster-management.io/manifest-work-cleanup`
   - ManifestWork blocks until AppliedManifestWork reports all resources are deleted
   - Check status conditions to see what's failing
   
2. **If ManagedCluster has deletionTimestamp:**
   - Check its finalizer: `cluster.open-cluster-management.io/api-resource-cleanup`
   - ManagedCluster blocks until associated resources are cleaned:
     - managedClusterAddons
     - manifestWorks in the cluster namespace
     - roleBindings for the klusterlet agent
   - Should be deleted after HostedCluster is gone

3. **If HostedCluster has deletionTimestamp:**
   - Check its finalizer: `hypershift.openshift.io/finalizer`
   - HostedCluster blocks until:
     - Control plane namespace is cleaned up
     - Azure resources in managed resource group are deleted
   - The Hypershift operator manages this cleanup

4. **If Control Plane Namespace has deletionTimestamp:**
   - Check what resources remain in the namespace
   - These resources are preventing namespace deletion

#### Step 5: Investigate Why the Resource is Stuck

For each stuck resource, investigate the controller's status:

```bash
# Check Hypershift operator logs
kubectl logs -n hypershift deployment/operator -f | grep ${CLUSTER_NAME}

# Check Maestro Agent logs (if ManifestWork is stuck)
kubectl logs -n maestro deployment/maestro-agent -c maestro-agent | grep ${CLUSTER_ID}

# Check work agent logs (manages ManifestWork application)
kubectl logs -n open-cluster-management-agent deployment/klusterlet-work-agent

# Check events for the resource
kubectl get events -n ocm-xxx-${CLUSTER_ID} --sort-by='.lastTimestamp' | grep ${CLUSTER_NAME}

# Check the resource's status conditions
kubectl get hostedcluster <cluster-name> -n ocm-xxx-${CLUSTER_ID} -o jsonpath='{.status.conditions}' | jq .

# Check ManifestWork applied status
kubectl get manifestwork <name> -n local-cluster -o jsonpath='{.status.conditions[?(@.type=="Applied")]}' | jq .
```

Look for:
- Error messages indicating what's failing
- Stuck conditions (e.g., "Waiting for...", "Failed to...")
- Resources referenced that may not exist
- External dependencies (Azure resources, DNS, networking)

### Phase 3: Resolution Strategies

#### Strategy 1: Wait and Monitor (Preferred)

Sometimes controllers are slow or retrying failed operations:

```bash
# Watch the resource for changes
kubectl get hostedcluster <cluster-name> -n ocm-xxx-${CLUSTER_ID} -w

# Monitor finalizers
kubectl get hostedcluster <cluster-name> -n ocm-xxx-${CLUSTER_ID} -o jsonpath='{.metadata.finalizers}' -w
```

Give the controllers at least 10-15 minutes to complete, especially if dealing with external resources (Azure, DNS).

#### Strategy 2: Fix the Underlying Issue

If logs or events reveal a specific problem:

1. **Missing Azure resources:** Resource may already be deleted externally
2. **Permission issues:** Controller may lack necessary RBAC or Azure permissions
3. **Orphaned resources:** Child resources may exist without proper owner references
4. **Network issues:** Controller may not be able to reach external APIs

Address the root cause when possible rather than forcing finalizer removal.

#### Strategy 3: Manual Cleanup of Child Resources

If a namespace won't delete because of remaining resources:

```bash
# List all resources in the namespace
kubectl api-resources --verbs=list --namespaced -o name | \
  xargs -n 1 kubectl get -n ocm-xxx-${CLUSTER_ID}-${CLUSTER_NAME}

# Identify resources with deletionTimestamp
# Delete them individually if needed
kubectl delete <resource-type> <resource-name> -n <namespace> --force --grace-period=0
```

**Warning**: Force deletion should be used sparingly and only when you understand the implications.

#### Strategy 4: Manual Finalizer Removal (Last Resort)

Only remove finalizers when:
- You've confirmed the controller is not running or is broken
- You've verified the resources the finalizer protects are properly cleaned up
- You've exhausted all other options
- You understand the consequences (possible resource leaks in Azure, etc.)

```bash
# Remove a finalizer from a resource
kubectl patch hostedcluster <cluster-name> -n ocm-xxx-${CLUSTER_ID} \
  --type=json -p='[{"op": "remove", "path": "/metadata/finalizers/0"}]'

# Or edit directly (opens editor)
kubectl edit hostedcluster <cluster-name> -n ocm-xxx-${CLUSTER_ID}
# Remove the finalizer from the list and save
```

**Critical**: Always remove finalizers in the correct order (bottom-up from leaf resources to top-level resources).

### Phase 4: Verify Cleanup Completion

#### Step 6: Confirm Resource Deletion

After taking action, verify that resources are being deleted:

```bash
# Check that the control plane namespace is gone
kubectl get namespace ocm-xxx-${CLUSTER_ID}-${CLUSTER_NAME}

# Check that the HostedCluster namespace is gone
kubectl get namespace ocm-xxx-${CLUSTER_ID}

# Check that ManifestWork is gone
kubectl get manifestwork -n local-cluster | grep ${CLUSTER_ID}

# Check that ManagedCluster is gone
kubectl get managedcluster | grep ${CLUSTER_ID}
```

#### Step 7: Verify Azure Resources

If the cluster had Azure resources in a managed resource group:

```bash
# Check if the managed resource group still exists
az group show --name <managed-resource-group-name>

# List resources in the managed resource group
az resource list --resource-group <managed-resource-group-name>
```

If Azure resources remain after cluster deletion:
- This may indicate a controller failure during cleanup
- Resources may need to be manually deleted via Azure portal or CLI
- Document the issue for investigation to prevent future occurrences

## Troubleshooting Commands Reference

### General Resource Inspection

```bash
# Get all resources with deletion timestamps in a namespace
kubectl get all,secrets,configmaps,pvc -n <namespace> --field-selector metadata.deletionTimestamp!=''

# Get finalizers for all resources of a type
kubectl get hostedcluster -A -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.finalizers}{"\n"}{end}'

# Watch namespace deletion
kubectl get namespace <namespace> -w

# Get all events in a namespace sorted by time
kubectl get events -n <namespace> --sort-by='.lastTimestamp'

# Check if a namespace has finalizers
kubectl get namespace <namespace> -o jsonpath='{.metadata.finalizers}'

# Get all CRDs related to Hypershift
kubectl get crd | grep hypershift

# Get all CRDs related to ACM/MCE
kubectl get crd | grep open-cluster-management
```

### ManifestWork Specific

```bash
# List all ManifestWork resources
kubectl get manifestwork -n local-cluster

# Check ManifestWork finalizer
kubectl get manifestwork <name> -n local-cluster \
  -o jsonpath='{.metadata.finalizers}' | grep manifest-work-cleanup

# Check if ManifestWork was successfully applied
kubectl get manifestwork <name> -n local-cluster \
  -o jsonpath='{.status.conditions[?(@.type=="Applied")]}'

# Check resource status feedback
kubectl get manifestwork <name> -n local-cluster \
  -o jsonpath='{.status.resourceStatus}' | jq .

# Check AppliedManifestWork
kubectl get appliedmanifestwork <name> -o yaml

# Remove ManifestWork finalizer (last resort)
kubectl patch manifestwork <name> -n local-cluster \
  --type=json -p='[{"op": "remove", "path": "/metadata/finalizers/0"}]'
```

### ManagedCluster Specific

```bash
# Check ManagedCluster finalizer
kubectl get managedcluster <name> \
  -o jsonpath='{.metadata.finalizers}'

# Check ManagedCluster status
kubectl get managedcluster <name> -o jsonpath='{.status}' | jq .

# List resources in the ManagedCluster namespace
kubectl get all,managedclusteraddon,manifestwork -n <cluster-name>
```

### HostedCluster Specific

```bash
# Check HostedCluster finalizer
kubectl get hostedcluster <name> -n ocm-xxx-${CLUSTER_ID} \
  -o jsonpath='{.metadata.finalizers}'

# Check HostedCluster status
kubectl get hostedcluster <name> -n ocm-xxx-${CLUSTER_ID} \
  -o jsonpath='{.status.conditions}' | jq .

# Check what resources HostedCluster created
kubectl get all -n ocm-xxx-${CLUSTER_ID}-${CLUSTER_NAME}
```

### Controller Logs

```bash
# Hypershift operator logs
kubectl logs -n hypershift deployment/operator -f

# Maestro Agent logs
kubectl logs -n maestro deployment/maestro-agent -c maestro-agent -f

# Work agent logs (manages ManifestWork)
kubectl logs -n open-cluster-management-agent deployment/klusterlet-work-agent -f
```

### Emergency Finalizer Removal

```bash
# Remove namespace finalizer (emergency only)
kubectl patch namespace <namespace> -p '{"metadata":{"finalizers":null}}' --type=merge

# Remove resource finalizer (replace index 0 with correct position)
kubectl patch <resource-type> <name> -n <namespace> \
  --type=json -p='[{"op": "remove", "path": "/metadata/finalizers/0"}]'
```

## Service Cluster Verification (Optional)

If you have access to the service cluster, you can check the Maestro Server side to understand the overall orchestration state:

```bash
# On the service cluster, check ResourceBundle status
kubectl get resourcebundle -n maestro | grep ${CLUSTER_ID}

# Check ResourceBundle conditions and status
kubectl get resourcebundle <bundle-name> -n maestro -o yaml

# Look at status feedback from management cluster
kubectl get resourcebundle <bundle-name> -n maestro \
  -o jsonpath='{.status}' | jq .

# Check Maestro Server logs
kubectl logs -n maestro deployment/maestro -f | grep ${CLUSTER_ID}
```

**Note**: ResourceBundles in the service cluster represent the desired state. The Maestro Agent on the management cluster receives these via MQTT and creates ManifestWork resources. Status flows back from AppliedManifestWork → ManifestWork → Maestro Agent → ResourceBundle.

If ResourceBundle shows it was successfully applied but ManifestWork is stuck, the issue is on the management cluster side.
