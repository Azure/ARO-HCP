# ARO-HCP Architecture

ARO-HCP (Azure Red Hat OpenShift — Hosted Control Planes) is a managed OpenShift service where customer
cluster control planes run on shared management clusters rather than on the customer's own infrastructure.

## Request Flow

A customer operation (e.g. create cluster) flows through the following components in order:

```
Customer → ARM → Frontend → Backend (async) → Clusters Service → Maestro → HyperShift
                                    └→ kube-applier (desires) ────────────────┘
```

1. **ARM** delivers the request to the **Frontend** (RP), which validates it and creates an async operation.
2. The **Backend** picks up the async operation, translates it into Clusters Service API calls, and may also
   write kube-applier desires to Cosmos for direct management-cluster changes. Over time, the backend will take
   over from CS; check the codebase for clear distinction of responsibilities.
3. **Clusters Service** (CS) manages the cluster/nodepool lifecycle via its own state machine, producing
   Maestro resource bundles that describe the desired state on the management cluster.
4. **Maestro server** delivers those bundles to the management cluster as ManifestWork objects.
5. **Maestro agent** applies the objects to the management cluster and pushes status back to the server.
6. **Kube Applier** (on each management cluster) reconciles backend `ApplyDesire` / `DeleteDesire` / `ReadDesire`
   documents from Cosmos against the local kube-apiserver.
7. **HyperShift** reconciles HostedCluster and NodePool custom resources on the management cluster,
   creating the actual control plane pods in a hosted-control-plane namespace.

## Deletion Flow

A customer deletion operation follows the same component chain, but cleanup on the management
cluster involves a sequential **destruct chain** managed by Clusters Service.

### Cluster Deletion

```
Customer → ARM → Frontend → Backend (async) → Clusters Service → Management Cluster cleanup
```

1. **ARM** delivers the DELETE request to the **Frontend**, which creates an async operation.
2. The **Backend** translates it into a Clusters Service API call.
3. **Clusters Service** sets the cluster state to `'uninstalling'` and runs the cluster destruct
   chain (see `aro-hcp-clusters-service` repo, `pkg/clusterprovisioner/acm/destruct/`):
   - `hypershift-managed-cluster-destructor`: waits for the **ManagedCluster** (ACM/MCE) CR
     to be fully deleted. Deletion requires **ManagedClusterAddon** pre-delete hook pods to
     complete and remove their finalizers first. If the ManagedCluster CR still exists, the
     destructor returns without advancing.
   - `hypershift-manifest-work-destructor`: deletes Maestro resource bundles, which removes
     ManifestWork objects, cascading to HostedCluster / NodePool / control plane deletion.
   - `break-glass-credential-secrets-deleter`: removes break-glass credential secrets.
   - `swift-podnetworkinstance-deleter`: removes PodNetworkInstance resources.
4. **HyperShift** reconciles the HostedCluster deletion, cleaning up the control plane namespace
   and cloud resources.

### Node Pool Deletion

Node pool deletion follows a simpler path with its own 3-step destruct chain
(see `aro-hcp-clusters-service` repo, `pkg/nodepoolprovisioner/acm/aro/destruct/`):

1. The API handler sets the node pool state to `'uninstalling'`. Once in this state, the node
   pool cannot transition to any other state.
2. The delete node pool worker picks it up and runs the destruct chain:
   - `nodePoolCrDestructor`: deletes the ManifestWork for the node pool from the service cluster.
     ACM propagates the deletion to the management cluster, where HyperShift drains nodes and
     deallocates Azure VMs. The destructor waits until the ManifestWork is fully gone.
   - `RhManagedNsgSubnetDissociationDestructor`: removes the RH-managed NSG association from the
     node pool's subnet (only if the subnet differs from the cluster default).
   - `nodePoolDbDestructor`: deletes the node pool record and associated resource key from the DB.

Unlike cluster deletion, node pool deletion does not involve ManagedCluster, addon pre-delete
hooks, or namespace cleanup — it only needs the ManifestWork removal, Azure networking cleanup,
and DB record deletion.

### Destruct Chain Behavior

Both cluster and node pool destruct chains are **sequential** and run as Go function calls within
the Clusters Service process (not as Kubernetes Jobs on the management cluster). Each chain
restarts from step 0 on each reconcile — completed steps return immediately since their resources
are already gone. If one destructor cannot complete (e.g. ManagedCluster CR stuck pending
deletion, or a ManifestWork still has a `DeletionTimestamp`), all subsequent destructors are
skipped. CS logs `destructor ... has not completed yet` or `Not continuing to the next destructor`
on each iteration. There is no built-in timeout — the chain retries indefinitely until the
blocking step resolves or an operator intervenes.

For cluster deletion stuck at the ManagedCluster step, check
`conditions/acm/managedClusterConditions` for the ManagedCluster state. For node pool deletion
stuck at the CR destructor step, check HyperShift NodePool conditions and ManifestWork status.

## Topology

- **Management clusters**: Run HyperShift, Maestro agent, kube-applier, and hosted control planes for many customers.
- **Service clusters**: Run the Frontend, Backend, Clusters Service, and Maestro server.
- Each environment has its own Kusto cluster with two databases:
  - **Service database**: Frontend, Backend, and Clusters Service logs.
  - **HCP database**: HyperShift, control-plane-operator, and per-cluster control plane logs.

## Resource Types

| ARM Resource Type | Description |
|---|---|
| `microsoft.redhatopenshift/hcpopenshiftclusters` | Top-level cluster resource |
| `microsoft.redhatopenshift/hcpopenshiftclusters/nodepools` | Node pool (child of cluster) |
| `microsoft.redhatopenshift/hcpopenshiftclusters/requestadmincredential` | Admin credential request |

Each cluster resource maps to downstream objects:
- A CS cluster ID (`cid`) (opaque string like `2iig1flm0pfjr9h8kkg6ggbjig1p3fpa`)
- Maestro bundle IDs and ManifestWork names
- A HyperShift HostedCluster in a namespace on the management cluster
- A HyperShift HostedControlPlane in its own namespace (`<hc-namespace>-<hc-name>`)

## Key Repositories

| Repository | Contains |
|---|---|
| `ARO-HCP` | Frontend, Backend, Admin API, tooling |
| `aro-hcp-clusters-service` | Clusters Service (private, mirrored from GitLab) |
| `maestro` | Maestro server and agent |
| `hypershift` | HyperShift operator |
