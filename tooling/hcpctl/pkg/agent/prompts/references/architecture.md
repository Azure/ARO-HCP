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
   write desires directly, and CS writes a few ReadDesires of its own (`csReadonly*`) to observe
   the objects its lifecycle depends on; check the deployed source code for the owner of each desire.
3. **Kube-applier**, running on each management cluster, reconciles those desires against
   the local kube-apiserver. `ApplyDesire` uses `spec.type=ServerSideApply` to apply an object
   or `spec.type=Delete` to delete one and wait for its finalizers. `ReadDesire` watches
   objects and returns their observed content and status through Cosmos.
4. **HyperShift** reconciles HostedCluster and NodePool custom resources on the management
   cluster, creating the actual control plane pods in a hosted-control-plane namespace.
5. The **Backend** consumes observed state from ReadDesires and mirrors relevant status onto
   ServiceProviderCluster and controller conditions. It sets async operation status from the
   worst of several sources: that observed HyperShift state, CS cluster or node pool status, and
   its own checks such as validation, placement, certificates, and role assignments.
   The frontend reads this persisted state; it does not contact management clusters directly.

The management-cluster delivery path is **Backend → Cosmos desires → kube-applier →
Kubernetes objects → HyperShift**. Status returns through **ReadDesire → Cosmos → Backend**
(or CS, for its own ReadDesires). Clusters Service remains a lifecycle and desired-resource
source while those responsibilities move into backend controllers; it is not the cross-cluster
delivery mechanism.

## Deletion Flow

The frontend records deletion. The backend then issues the Clusters Service delete call and,
independently, starts tearing down its kube-applier desires. The two resulting sequential chains
run concurrently and meet at the HostedCluster: the backend removes management-cluster objects,
and CS cleans up Azure and service-side resources once the HostedCluster is gone. In both
chains a blocked step prevents later steps from running and is retried on later reconciles.
Inspect `backend/pkg/controllers/clusterresources/apply_desire_removal_chain.go` and the CS
`pkg/clusterprovisioner/acm/aro/destruct/chain.go` for the current order.

### Cluster Deletion

The backend's `ClusterResources` controller tears down its owned ApplyDesires in order:

1. Stop reconciling objects covered by HostedCluster teardown or namespace deletion, including
   NodePools, by dropping their desire documents.
2. Delete the ManagedCluster (only older clusters have one; new clusters no longer get ACM
   ingress wiring) and then shared ingress manifests, waiting for each step.
3. Delete the HostedCluster and wait for HyperShift to finish its finalizer, control plane,
   and cloud-resource cleanup while the required namespaces still exist.
4. Delete SWIFT networking resources in reverse creation order.
5. Delete the cluster namespaces last.

For objects that need explicit deletion, the backend sets `ApplyDesire.spec.type=Delete`.
Kube-applier deletes the object and waits for finalizers. The backend must confirm
`SuccessfullyDeleted=True` before removing the desire from Cosmos; legacy `Successful` is not
a valid fallback for delete completion. **Removing an ApplyDesire document alone does not delete its Kubernetes object**;
it only stops reconciliation. A live apply desire can recreate an object deleted manually.

Clusters Service sets the cluster to `uninstalling` and runs its destruct chain in-process,
restarting from the first step on each reconcile:

1. `swift-sal-deleter`, `rh-managed-nsg-deleter`, and `managed-resource-group-deleter` each wait
   until CS's `csReadonlyHypershiftHostedCluster` ReadDesire no longer observes the HostedCluster.
   This is an observation-based gate, not proof that backend step 3 completed: CS treats a missing
   ReadDesire or empty `kubeContent` as absent without checking `Successful`.
2. `read-desires-deleter` removes CS's own ReadDesires.
3. `dns-recordsets-deleter`, `container-registry-token-deleter`, `oidc-config-deleter`,
   `data-plane-identity-unfederator`, `managed-identities-secrets-deleter`, and
   `tls-certificate-deleter` clean up Azure resources, then `archiver` archives the cluster.

For stuck deletion, first find the blocked chain. CS logs `Destructor '<name>' did not finish.
Not continuing to the next destructor` in `clustersServiceLogs`. If it is waiting on the
HostedCluster, check both backend deletion progress and the health of CS's HostedCluster
ReadDesire: its mirrored content may be stale after the object is gone. Correlate backend
controller conditions, kube-applier logs, ReadDesire state, Kubernetes events, and the target
object's finalizers. ManagedCluster addon pre-delete hooks can still block its removal; check
`conditions/acm/managedClusterConditions` when that object exists.

### Node Pool Deletion

The backend issues the Clusters Service node pool delete without waiting on desires. Once the
node pool has a deletion timestamp, the `ClusterResources` controller treats its NodePool
ApplyDesire as stale, switches it to `spec.type=Delete`, waits for `SuccessfullyDeleted=True`,
and then purges the ApplyDesire and its ReadDesire. HyperShift handles node draining and Azure VM
deallocation when the NodePool is deleted.

Meanwhile CS runs its node pool destruct chain: `node-pool-cr-destructor` removes the NodePool
from CS cluster resources, `node-pool-rh-managed-nsg-subnet-dissociation-destructor` detaches
the RH-managed NSG when the node pool subnet differs from the cluster subnet,
`node-pool-read-desire-deleter` removes CS's NodePool ReadDesire, and
`node-pool-destructor-from-db` deletes the record. A blocked step logs `destructor '<name>' ...
has not completed yet`. Correlate kube-applier reconciliation of the NodePool desire with
NodePool conditions and finalizers, and CS logs for the destruct chain.

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
