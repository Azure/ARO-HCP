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
cluster involves a sequential **destruct chain** managed by Clusters Service:

```
Customer → ARM → Frontend → Backend (async) → Clusters Service → Management Cluster cleanup
```

1. **ARM** delivers the DELETE request to the **Frontend**, which creates an async operation.
2. The **Backend** translates it into a Clusters Service API call.
3. **Clusters Service** sets the cluster state to `'uninstalling'` and runs the destruct chain:
   - `hypershift-managed-cluster-destructor`: waits for the **ManagedCluster** (ACM/MCE) to
     finish `Detaching`. Detaching triggers cleanup of **ManagedClusterAddon** resources whose
     pre-delete hook pods must complete and remove their finalizers before the ManagedCluster
     can be deleted.
   - `hypershift-manifest-work-destructor`: deletes Maestro resource bundles, which removes
     ManifestWork objects, cascading to HostedCluster / NodePool / control plane deletion.
4. **HyperShift** reconciles the HostedCluster deletion, cleaning up the control plane namespace
   and cloud resources.

The destruct chain is **sequential**: if one destructor cannot complete (e.g. ManagedCluster stuck
in `Detaching`), all subsequent destructors are skipped. CS logs `Not continuing to the next
destructor for cluster` on each iteration until the blocking destructor resolves.

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

## Kubernetes events

The `kube-events` collector on each service and management cluster ingests Kubernetes API **Event** objects into
**`ServiceLogs.kubernetesEvents`**. Filter by `cluster`, `eventNamespace`, `objectKind`, `objectName`, `reason`, and
`message`. Snapshot summaries (`hypershift/controlPlaneEvents`, `hypershift/events`, `maestro/events`, …) all query
this table in the Service database.

This is not mgmt-agent output: K8s Events report API-level reasons (mount failures, scheduling, probes). Use mgmt-agent
`pod event` logs when you need container waiting/termination timelines that Events do not capture.

## mgmt-agent resource snapshots and pod state change logs (management cluster)

mgmt-agent runs on each management cluster. Besides reconcilers (SWIFT NIC capacity on nodes, optional
kube-state-metrics per HCP), it includes two **watch-only** informers that log full object snapshots to
Kusto on every relevant change:

| Watcher | Log message | When emitted | Typical use |
|---|---|---|---|
| **ResourceWatcher** | `resource event` | Add/Update/Delete on discovered API groups (Hypershift, ACM, CAPI, `multitenancy.acn.azure.com`, …) and core `v1/namespaces` | CR status timelines — e.g. `PodNetworkInstance` readiness; HCP namespace lifecycle (HostedCluster CR namespace + HCP control-plane namespace per cluster) |
| **PodWatcher** | `pod event` | Pod add/delete; pod update when a container's state **type** changes (`waiting`→`running`, etc.) | Control plane pod lifecycle without scraping container logs |

PodWatcher does not emit on field-level changes within the same state type (for example a new `waiting.reason` while still `waiting`).

Both log from container `mgmt-agent-controller` into the **Service** Kusto database (`containerLogs` table,
`cluster` = management cluster name). Fields include `log.event`, `log.namespace`, `log.name`, and a full
`log.object` payload.

## Key Repositories

| Repository | Contains |
|---|---|
| `ARO-HCP` | Frontend, Backend, Admin API, tooling |
| `aro-hcp-clusters-service` | Clusters Service (private, mirrored from GitLab) |
| `maestro` | Maestro server and agent |
| `hypershift` | HyperShift operator |
