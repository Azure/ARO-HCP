# Service Components

## Frontend (`arohcpfrontend`)

The ARM-facing resource provider. Receives customer HTTP requests (PUT, POST, PATCH, DELETE) for
clusters and node pools.

**Responsibilities:**
- Request validation and authentication
- Creating async operations (returns `Azure-AsyncOperation` header with operation URL)
- Proxying GET requests

**Failure modes:** Validation errors (4xx), timeout creating async operation, panic/crash.

## Backend (`arohcpbackend`)

Processes async operations created by the Frontend. Runs as a set of controllers.

**Responsibilities:**
- Polling async operation status
- Translating ARM operations into Clusters Service API calls
- Polling the CS cluster resources API and writing ApplyDesire / ReadDesire documents to Cosmos
- Consuming observed Kubernetes state from ReadDesires and mirroring status onto ServiceProviderCluster
- Coordinating ordered deletion through kube-applier desires
- Dumping resource state periodically (the `datadump` and `csstatedump` controllers)

**Failure modes:** CS API errors, stale state, controller crash loops, provisioning state stuck.

## Clusters Service (`aro-hcp-clusters-service`)

Red Hat-hosted service that manages cluster and node pool lifecycle responsibilities that have
not yet moved into backend controllers. Check the deployed source code for the current boundary.

**Responsibilities:**
- Maintains its own state machine for cluster/nodepool provisioning
- Interacts with Azure to provision infrastructure required for a cluster
- Exposes desired management-cluster resources through the cluster resources API, consumed by
  the backend and delivered through kube-applier
- Observes the management-cluster objects it depends on through its own `csReadonly*` ReadDesires
- Cleans up Azure and service-side resources in sequential cluster and node pool destruct chains;
  cluster cleanup starts once its HostedCluster ReadDesire no longer observes the HostedCluster

**Failure modes:** State machine stuck, desired-resource generation failure, blocked destruct chain,
internal errors, dependency on
external services (e.g. DNS, certificate provisioning).

## Kube Applier (`kube-applier`)

Bridge from the backend (service cluster) to the management cluster kube-apiserver without direct
cross-cluster API access.

**Responsibilities:**
- Reconciles `ApplyDesire` documents from the backend and `ReadDesire` documents from the backend
  and Clusters Service in Cosmos (partitioned by management-cluster resource ID)
- Applies or deletes objects according to `ApplyDesire.spec.type` (`ServerSideApply` / `Delete`)
- Watches local Kubernetes objects through ReadDesires and writes observed content/status to Cosmos
- Reports apply/delete outcome through `SuccessfullyApplied` / `SuccessfullyDeleted` and
  controller health through `Degraded`; ReadDesires report observation through `Successful`

**Key identifiers:**
- Desire documents in a dedicated Cosmos container for each management cluster; the partition key
  is the lowercased management-cluster resource ID. Kube-applier credentials are scoped to its
  own container, while the backend and Clusters Service use per-management-cluster clients.
- Parent resource ID linking a desire back to the HCP cluster or node pool

**Interpreting status:**
- `SuccessfullyApplied=True` means the Kubernetes apply call succeeded. Assess resource readiness
  separately through the target object's conditions and the responsible operator's state.
- Delete completion requires `SuccessfullyDeleted=True`, which means the target is absent and
  its finalizers have completed. Never fall back to legacy `Successful` for deletion.
- ReadDesire `Successful=True` means the informer synced and `status.kubeContent` reflects its
  last observation. Nil content with `Successful=True` means observed absence; nil content alone
  may mean the object has not been observed yet. ReadDesire has no explicit observed-at timestamp
  or contractual refresh guarantee, so correlate mirrored state with Kubernetes events and logs.

**Failure modes:** Apply/delete completion condition remains false, ReadDesire `Successful=False`,
`Degraded=True`, kube API errors (`KubeAPIError`), pre-check failures, or orphaned desires after parent deletion.

## HyperShift

Operator running on management clusters that reconciles HostedCluster and NodePool custom resources
into actual control plane infrastructure.

**Responsibilities:**
- Creating hosted control plane pods (kube-apiserver, etcd, etc.)
- Managing NodePool machine sets
- Reporting conditions on HostedCluster and NodePool objects

**Key objects:**
- `HostedCluster` in a namespace on the management cluster
- `HostedControlPlane` in `<namespace>-<name>` namespace
- `NodePool` in the same namespace as the HostedCluster

**Failure modes:** Condition degraded/progressing stuck, pod crash loops, etcd issues, RBAC errors,
resource quota exceeded, image pull failures.

## ACM / ManagedCluster Layer

Advanced Cluster Management (ACM) and Multicluster Engine (MCE) components manage cluster
registration and addon lifecycle on the management cluster. Only older clusters are registered
as ManagedClusters; new clusters no longer get ACM ingress wiring.

**Key objects:**
- `ManagedCluster` (cluster-scoped): represents a registered cluster, named by the CS cluster ID.
  Finalizer: `cluster.open-cluster-management.io/api-resource-cleanup`.
- `ManagedClusterAddon` (namespaced under the cluster ID): represents an addon installed on the
  managed cluster. Common addons: `config-policy-controller`, `governance-policy-framework`.
  Finalizers: `hosting-manifests-cleanup`, `hosting-addon-pre-delete`.

**Responsibilities:**
- Cluster registration and status reporting
- Addon lifecycle management (install, upgrade, pre-delete hooks)

**Role in deletion:**
The backend's ApplyDesire removal chain deletes the ManagedCluster through kube-applier and
waits for the object to disappear. The ManagedCluster enters `Detaching` state, triggering
addon pre-delete hooks. Each
addon's pre-delete hook pod must complete and remove its finalizers before the ManagedCluster
can finish detaching. Until all addons are cleaned up, the deletion chain is blocked.

**Failure modes:**
- Addon pre-delete pod eviction: node resource pressure (MemoryPressure, DiskPressure) evicts
  the pre-delete hook pods before completion, leaving finalizers in place.
- Klusterlet auth loss: the klusterlet identity loses Azure AD authorization during Detaching,
  preventing pre-delete hooks from executing.
- ManagedCluster stuck Detaching: any addon cleanup failure leaves the ManagedCluster in
  Detaching state indefinitely, blocking the entire deletion chain.

**Key logs:**
- Pod lifecycle (including eviction): `containerLogs` from `mgmt-agent` namespace —
  `log.msg == 'pod event'` and `log.namespace == 'klusterlet-<cluster-id>'`. Evicted pods
  have `log.object.status.reason == 'Evicted'` with `log.object.status.message` containing
  the node condition (e.g. `"The node was low on resource: memory"`).
- Deletion chain: backend `ClusterResources` controller logs and `kubeApplierLogs` — correlate
  desire resource IDs, target objects, waiting conditions, and finalizers to locate the blocked step.
