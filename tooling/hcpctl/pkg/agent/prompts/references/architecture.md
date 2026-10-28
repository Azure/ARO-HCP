# ARO-HCP Architecture

ARO-HCP (Azure Red Hat OpenShift — Hosted Control Planes) is a managed OpenShift service where customer
cluster control planes run on shared management clusters rather than on the customer's own infrastructure.

## Request Flow

1. **ARM** delivers the request to the **Frontend** (RP), which validates it, writes the
   resource to Cosmos, and creates an async operation.
2. The **Backend** processes the operation and calls **Clusters Service** (CS) for lifecycle
   operations that CS still owns. CS exposes the desired Kubernetes resources through its
   cluster resources API. The backend's `ClusterResources` controller polls that API and
   writes `ApplyDesire` and `ReadDesire` documents to Cosmos. Other backend controllers also
   write desires directly; check the deployed source code for the owner of each resource.
3. **Kube-applier**, running on each management cluster, reconciles those desires against
   the local kube-apiserver. `ApplyDesire` uses `spec.type=ServerSideApply` to apply an object
   or `spec.type=Delete` to delete one and wait for its finalizers. `ReadDesire` watches
   objects and returns their observed content and status through Cosmos.
4. **HyperShift** reconciles HostedCluster and NodePool custom resources on the management
   cluster, creating the actual control plane pods in a hosted-control-plane namespace.
5. The **Backend** consumes observed state from ReadDesires, mirrors relevant status onto
   ServiceProviderCluster and controller conditions, and updates async operation status.
   The frontend reads this persisted state; it does not contact management clusters directly.

The management-cluster delivery path is **Backend → Cosmos desires → kube-applier →
Kubernetes objects → HyperShift**. Status returns through **ReadDesire → Cosmos → Backend**.
Clusters Service remains a lifecycle and desired-resource source while those responsibilities
move into backend controllers; it is not the cross-cluster delivery mechanism.

## Deletion Flow

The frontend records deletion and the backend coordinates lifecycle cleanup, including any
Clusters Service API calls still required by the deployed version. Kubernetes cleanup uses
kube-applier desires. Inspect the backend's `ClusterResources` controller and
`backend/pkg/controllers/clusterresources/apply_desire_removal_chain.go` for the current order.

### Cluster Deletion

The backend tears down its owned ApplyDesires in a sequential chain:

1. Stop reconciling objects covered by HostedCluster teardown or namespace deletion, including
   NodePools, by dropping their desire documents.
2. Delete the ManagedCluster and then shared ingress manifests, waiting for each step.
3. Delete the HostedCluster and wait for HyperShift to finish its finalizer, control plane,
   and cloud-resource cleanup while the required namespaces still exist.
4. Delete SWIFT networking resources in reverse creation order.
5. Delete the cluster namespaces last.

For objects that need explicit deletion, the backend sets `ApplyDesire.spec.type=Delete`.
Kube-applier deletes the object and waits for finalizers. The backend must confirm
`SuccessfullyDeleted=True` before removing the desire from Cosmos; legacy `Successful` is not
a valid fallback for delete completion. **Removing an ApplyDesire document alone does not delete its Kubernetes object**;
it only stops reconciliation. A live apply desire can recreate an object deleted manually.

A blocked step prevents later steps from running; the backend retries on subsequent reconciles.
For stuck deletion, correlate backend controller conditions, kube-applier logs, ReadDesire state,
Kubernetes events, and the target object's finalizers. ManagedCluster addon pre-delete hooks can
still block its removal; check `conditions/acm/managedClusterConditions` when that object exists.

### Node Pool Deletion

The backend coordinates node pool lifecycle cleanup and removes the node pool's desired resources
through kube-applier. HyperShift handles node draining and Azure VM deallocation when the NodePool
is deleted. Check the deployed backend and CS code for remaining lifecycle responsibilities,
then correlate kube-applier reconciliation with NodePool conditions and finalizers.

## Topology

- **Management clusters**: Run HyperShift, kube-applier, and hosted control planes for many customers.
- **Service clusters**: Run the Frontend, Backend, Clusters Service, and fleet.
- Each environment has its own Kusto cluster with service, control-plane, and monitoring databases:
  - **Service database**: Frontend, Backend, Clusters Service, kube-applier, and HyperShift operator logs.
  - **Monitoring database**: Prometheus/metric alert events.
  - **HCP database**: Control-plane-operator and per-cluster control plane logs.

## Resource Types

| ARM Resource Type | Description |
|---|---|
| `microsoft.redhatopenshift/hcpopenshiftclusters` | Top-level cluster resource |
| `microsoft.redhatopenshift/hcpopenshiftclusters/nodepools` | Node pool (child of cluster) |
| `microsoft.redhatopenshift/hcpopenshiftclusters/requestadmincredential` | Admin credential request |

Each cluster resource maps to downstream objects:
- A CS cluster ID (`cid`) (opaque string like `2iig1flm0pfjr9h8kkg6ggbjig1p3fpa`)
- Backend ApplyDesire and ReadDesire IDs, linked to the parent ARM resource
- A HyperShift HostedCluster in a namespace on the management cluster
- A HyperShift HostedControlPlane in its own namespace (`<hc-namespace>-<hc-name>`)

## Key Repositories

| Repository | Contains |
|---|---|
| `ARO-HCP` | Frontend, Backend, kube-applier, Admin API, tooling |
| `aro-hcp-clusters-service` | Clusters Service (private, mirrored from GitLab) |
| `hypershift` | HyperShift operator |
