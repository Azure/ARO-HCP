# 03 &mdash; Database wiring (`internal/database`)

## Goals

1. Give each management cluster its **own** Cosmos container. A kube-applier
   pod's Cosmos credentials are scoped to that single per-MC container, so
   an escape from one management cluster cannot read or write any other
   management cluster's `*Desire` documents.
2. Provide a per-container `KubeApplierDBClient` with `ResourceCRUD[T]`
   accessors (one per `*Desire` type), per-container `Listers()` for feeding
   informers, and a per-container `UntypedCRUD` for cross-cutting cleanup.
3. Provide a thread-safe `KubeApplierDBClients` (plural) registry that
   resolves each management-cluster resourceID to its per-MC
   `KubeApplierDBClient` on demand. Resolution walks a
   `ManagementClusterLister` to find the matching `fleet.ManagementCluster`
   document and uses its `Status.KubeApplierCosmosContainerName` /
   `Status.MaestroConsumerName`. The backend holds one of these so it can
   talk to every management cluster; the kube-applier sidecar binary opens
   its own single container directly and does not need the registry.
4. Fully isolate the kube-applier types from the existing `DBClient` / `GlobalListers`
   so an audit can see that the kube-applier credential surface cannot reach
   `Resources`, `Billing`, or any other container the binary should not touch.

Reference files:

- `internal/database/kube_applier_client.go` &mdash; `KubeApplierDBClient`,
  `KubeApplierListers`, `KubeApplierDBClients`, `ResourceParent`, constructors.
- `internal/database/crud_kube_applier.go` &mdash; per-MC `ResourceCRUD`
  implementation; partition key is the management cluster's name.
- `internal/database/crud_untyped_kube_applier.go` &mdash; cross-partition
  `UntypedCRUD` for the cleanup pass.
- `internal/databasetesting/mock_kube_applier_client.go` &mdash; mocks for
  both the singular and plural clients.

## Key design constraint &mdash; do not extend `DBClient` or `GlobalListers`

Reusing `DBClient` for the kube-applier would compile fine but would expose
methods (`HCPClusters`, `Operations`, &hellip;) that fail at runtime against
a real container the binary cannot read. That is a pit of failure we design
out at the type level by keeping two separate client surfaces:

| Client                              | Containers it touches                                            | Used by                                                                                   |
| ---                                 | ---                                                              | ---                                                                                       |
| `DBClient` (existing)               | `Resources`, `Billing`, `Locks` &mdash; **not** any kube-applier | frontend, backend, admin, etc.                                                            |
| `KubeApplierDBClient`               | one management cluster's container                               | the kube-applier sidecar binary                                                           |
| `KubeApplierDBClients` (plural)     | every configured management cluster's container                  | the backend &mdash; writes `*Desire`s and runs the orphan sweep across every container    |

## Client surface

```go
// Per-management-cluster handle. Each instance is bound to one Cosmos container
// at construction; methods never take a management cluster argument.
type KubeApplierDBClient interface {
    ApplyDesires(parent ResourceParent) (ResourceCRUD[kubeapplier.ApplyDesire], error)
    ReadDesires(parent ResourceParent) (ResourceCRUD[kubeapplier.ReadDesire], error)

    // Listers across this container (one MC's worth of data).
    Listers() KubeApplierListers

    // UntypedCRUD for cross-cutting cleanup; queries are over this container only.
    UntypedCRUD(parentResourceID azcorearm.ResourceID) (UntypedResourceCRUD, error)
}

type KubeApplierListers interface {
    ApplyDesires() GlobalLister[kubeapplier.ApplyDesire]
    ReadDesires() GlobalLister[kubeapplier.ReadDesire]
}

// Thread-safe registry of per-MC clients. Backend constructs one of these from a
// ManagementClusterLister; each For() call walks the lister to find the matching
// fleet.ManagementCluster and reads its container name and partition key off the
// status fields.
type KubeApplierDBClients interface {
    // For returns the client for this management cluster, constructing it on
    // demand. nil if no MC matches the rid, if the lister errors, or if the
    // matched MC has no container name configured.
    For(managementClusterResourceID *azcorearm.ResourceID) KubeApplierDBClient

    // ManagementClusterResourceIDs returns the resourceID of every MC currently
    // reported by the lister (unordered). Used by the orphan-cleanup controller
    // to iterate every container.
    ManagementClusterResourceIDs() []*azcorearm.ResourceID
}

// Narrow lister shape KubeApplierDBClients depends on; listers.ManagementClusterLister
// satisfies it.
type ManagementClusterLister interface {
    List(ctx context.Context) ([]*fleet.ManagementCluster, error)
}

// ResourceParent identifies what the *Desires are nested under.
// Either a cluster (NodePoolName == "") or a nodepool under a cluster.
type ResourceParent struct {
    SubscriptionID    string
    ResourceGroupName string
    ClusterName       string
    NodePoolName      string // optional
}

// Constructors.
func NewKubeApplierDBClient(container *azcosmos.ContainerClient, managementClusterPartitionKey string) KubeApplierDBClient
func NewKubeApplierDBClientFromDatabase(database *azcosmos.DatabaseClient, containerName, managementClusterPartitionKey string) (KubeApplierDBClient, error)
func NewKubeApplierDBClients(database *azcosmos.DatabaseClient, mcLister ManagementClusterLister) KubeApplierDBClients

// NewDBBackedManagementClusterLister adapts a FleetDBClient's GlobalListers
// into the narrow ManagementClusterLister; backends that don't yet have
// informers wired can use this directly.
func NewDBBackedManagementClusterLister(fleetClient FleetDBClient) ManagementClusterLister
```

## Implementation notes

- The kube-applier binary calls `NewKubeApplierDBClientFromDatabase(db, containerName, mcName)`
  with credentials scoped to its single container.
- The backend calls `NewKubeApplierDBClients(db, mcLister)`. The lister can be
  the informer-backed `listers.ManagementClusterLister` or the DB-backed
  `NewDBBackedManagementClusterLister(fleetClient)` adapter; both satisfy the
  narrow interface. Each `For()` consults the lister to find the matching MC
  (linear scan, expected to be small) and reads
  `Status.KubeApplierCosmosContainerName` for the container name and
  `Status.MaestroConsumerName` for the partition key. The constructed per-MC
  client is cached under a `sync.Mutex`; the MC set itself is re-read each
  call so fleet additions/removals become visible without restarting the
  backend.
- `ManagementClusterResourceIDs()` always re-queries the lister so the
  orphan-cleanup controller's iteration reflects the current fleet on each
  sync cycle.
- CRUD impls reuse the existing low-level helpers (`get`, `list`,
  `deleteResource`, etc.) for read paths; for create/replace they use the
  kube-applier-aware helpers (`createKubeApplier`, `replaceKubeApplier`,
  &hellip;) that validate the caller-supplied partition key matches the
  `*Desire`'s `Spec.ManagementCluster`. There is still only ever one partition
  value used per container, but Cosmos requires a key so the validation stays.
- `UntypedCRUD.Get` and `UntypedCRUD.Delete(resourceID)` intentionally return
  errors on the kube-applier UntypedCRUD: a `*Desire`'s resourceID does not
  encode the management cluster, so the partition key cannot be derived from
  it. Cleanup callers must use `DeleteByCosmosID(partitionKey, cosmosID)`
  with the partitionKey from the row they just listed.
- `KubeApplierListers` impls union the cluster- and node-pool-scoped resource
  types in a single query against the one container.

## Cosmos document wrapper

We continue to use `GenericDocument[T]` as the cosmos envelope, and the
type-switch in `convert_any.go` routes the two `*Desire` types through
`InternalToCosmosKubeApplier[T]`, which sets `partitionKey` from
`spec.managementCluster`. This was the model in the single-container era and
remains valid in the per-MC world: it just so happens that every document in
a per-MC container shares the same partition key.

## Tests

- Round-trip create/get tests on `MockKubeApplierDBClient` prove the
  partition key carried in the cosmos envelope is the management cluster.
- A cross-type listers test confirms that `Listers().ApplyDesires().List()`
  unions cluster- and node-pool-scoped resource types in one query.
- `kube_applier_clients_test.go` covers the plural registry: unknown
  resourceIDs return nil; the lister drives
  `ManagementClusterResourceIDs()`; the For()-iterates-lister branch returns
  nil cleanly when no MC matches; concurrent `For()` calls under `-race` do
  not race the cache or the lister.
- The backend's `deleteOrphanedCosmosResources` test exercises the full
  iteration: a registry of two per-MC mock clients, with desires across both
  containers, surviving live parents and getting deleted under missing ones.

## Mocks &mdash; `internal/databasetesting`

- `MockDBClient` (existing) is unchanged. It does **not** know about kube-applier.
- `MockKubeApplierDBClient` is the in-memory implementation of
  `KubeApplierDBClient` for a single management cluster's container. It owns
  its own document store. `NewMockKubeApplierDBClient()` and
  `NewMockKubeApplierDBClientWithResources(ctx, []any{...})` are the public
  entry points.
- `MockKubeApplierDBClients` (plural) is the in-memory registry. Tests call
  `Register(rid, mockKubeApplierDBClient)` to add per-MC entries; `For(rid)`
  returns the registered client (or nil for unknown rids); thread-safe.
- A small `mockDocumentStore` interface lets the existing `mockResourceCRUD[T]`
  machinery be reused by the kube-applier mocks without copying CRUD code.

## Risks / things to watch

- **Cross-container atomicity.** Cosmos `TransactionalBatch` is per-partition
  and per-container, so the backend cannot atomically write a `*Desire` and a
  `Resources`-container document in one shot &mdash; nor can it atomically
  span two MCs' containers. The backend must be designed to tolerate
  intermediate states. (This is consistent with current ARO-HCP behaviour.)
- **Configuration freshness.** The `KubeApplierDBClients` registry consults
  its `ManagementClusterLister` on every `For()` and
  `ManagementClusterResourceIDs()` call, so adding a management cluster to
  the fleet becomes visible on the next sweep without restarting the
  backend. Per-MC azcosmos client construction is cached, but cache entries
  for removed MCs are simply never returned by `ManagementClusterResourceIDs()`
  again.
- **Indexing policy.** Confirm each new container's indexing policy (auto vs.
  custom) before landing &mdash; cross-type listers query on `resourceType`.
- **Container creation pipeline.** Each per-MC container is created by IaC
  (bicep). Provisioning the containers and writing each container's name
  into the corresponding `fleet.ManagementCluster.Status.KubeApplierCosmosContainerName`
  is in scope for the rollout doc, not for the database client PR itself.
- **Two clients in the backend process.** The backend will hold both a
  `DBClient` and a `KubeApplierDBClients`. They are intentionally independent;
  they are not joined under a parent struct. Any future cross-cutting
  concern (metrics, tracing) must be wired in twice.
