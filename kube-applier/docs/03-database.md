# 03 &mdash; Database wiring (`internal/database`)

## Goals

1. Make a new Cosmos container, `kube-applier`, available as a *standalone*
   client. The kube-applier binary's Cosmos credentials are scoped to that
   single container (and ultimately a single partition within it), so the
   binary must not be able to reach any other container at the type level.
2. Provide single-partition `ResourceCRUD[T]` accessors keyed by management
   cluster (one accessor per `*Desire` type).
3. Provide a cross-partition `GlobalLister[T]` for each `*Desire` type so the
   backend can list across all management clusters.
4. Fully isolate the partition-key strategy (mgmt cluster name) from the
   existing subscription-ID strategy.

Reference files:

- `internal/database/database.go:34-38` &mdash; container constants.
- `internal/database/database.go:72-74` &mdash; `NewPartitionKey`.
- `internal/database/crud_nested_resource.go:30-62` &mdash; generic
  `ResourceCRUD` and constructor.
- `internal/database/crud_hcpcluster.go:117-212` &mdash; nested CRUD pattern.
- `internal/database/global_lister.go:38-82` &mdash; `GlobalListers`
  interface and per-type `cosmosGlobalLister`.

## Key design constraint &mdash; do not extend `DBClient` or `GlobalListers`

The kube-applier binary will be issued Cosmos credentials that grant it
**only** access to the `kube-applier` container, scoped to its own management
cluster's partition. Reusing the `DBClient` interface for it would compile
just fine but would offer methods (`HCPClusters`, `Operations`, &hellip;)
that would fail at runtime against a real container the binary cannot read.
That is a pit of failure we want to design out at the type level.

So we keep two completely separate client surfaces:

| Client                | Containers it touches | Used by |
| --- | --- | --- |
| `DBClient` (existing) | `Resources`, `Billing`, `Locks` &mdash; **not** `kube-applier` | frontend, backend, admin, etc. |
| `KubeApplierDBClient` (new) | `kube-applier` only | the kube-applier binary, *and* the backend (which holds wider creds and uses it to write `*Desire`s) |

The same applies to the cross-partition listers: existing
`database.GlobalListers` stays untouched; a new
`KubeApplierGlobalListers` lives on `KubeApplierDBClient`.

## Work items

### 3.1 Add the container constant + partition-key helper

Edit `internal/database/database.go`:

```go
const (
    billingContainer     = "Billing"
    locksContainer       = "Locks"
    resourcesContainer   = "Resources"
    kubeApplierContainer = "kube-applier"
)
```

Add a sibling helper to `NewPartitionKey`:

```go
// NewKubeApplierPartitionKey builds the partition key for the kube-applier
// container, which is partitioned by the lower-cased management cluster name.
func NewKubeApplierPartitionKey(managementCluster string) azcosmos.PartitionKey {
    return azcosmos.NewPartitionKeyString(strings.ToLower(managementCluster))
}
```

This lives next to `NewPartitionKey` so the deviation is visible to anyone
auditing partition strategy. The constant and helper are shared between the
existing client and the new one but no other change is made to
`cosmosDBClient` or `NewDBClient`.

### 3.2 New file: `kube_applier_client.go`

Define the standalone client surface:

```go
// KubeApplierDBClient is the database surface used by the kube-applier binary.
// It is intentionally narrower than DBClient because the kube-applier pod's
// Cosmos credentials are scoped to a single container.
type KubeApplierDBClient interface {
    // KubeApplier returns CRUD accessors scoped to a single management-cluster
    // partition.
    KubeApplier(managementCluster string) KubeApplierCRUD

    // GlobalListers returns cross-partition listers for the *Desire types.
    // Only callers with container-wide credentials (the backend) should use this.
    GlobalListers() KubeApplierGlobalListers
}

type KubeApplierCRUD interface {
    ApplyDesires(parent ResourceParent) (ResourceCRUD[kubeapplier.ApplyDesire], error)
    DeleteDesires(parent ResourceParent) (ResourceCRUD[kubeapplier.DeleteDesire], error)
    ReadDesires(parent ResourceParent) (ResourceCRUD[kubeapplier.ReadDesire], error)
}

type KubeApplierGlobalListers interface {
    ApplyDesires() GlobalLister[kubeapplier.ApplyDesire]
    DeleteDesires() GlobalLister[kubeapplier.DeleteDesire]
    ReadDesires() GlobalLister[kubeapplier.ReadDesire]
}

// ResourceParent identifies what the *Desires are nested under.
// Either a cluster (NodePool == "") or a nodepool under a cluster.
type ResourceParent struct {
    SubscriptionID    string
    ResourceGroupName string
    ClusterName       string
    NodePoolName      string // optional
}

// NewKubeApplierDBClient instantiates a KubeApplierDBClient from a Cosmos
// DatabaseClient. It opens *only* the kube-applier container.
func NewKubeApplierDBClient(database *azcosmos.DatabaseClient) (KubeApplierDBClient, error)

// NewKubeApplierDBClientFromContainer wraps an already-opened container;
// useful when the caller has constructed the container client itself.
func NewKubeApplierDBClientFromContainer(kubeApplier *azcosmos.ContainerClient) KubeApplierDBClient
```

Implementation notes:

- The kube-applier binary calls `NewKubeApplierDBClient` with credentials
  scoped to the kube-applier container.
- The backend, which already builds an `azcosmos.DatabaseClient` with
  broader credentials, *also* calls `NewKubeApplierDBClient` to get the
  kube-applier surface. It does not reach kube-applier through `DBClient`.
- The CRUD impl reuses the existing low-level helpers (`get`, `list`,
  `deleteResource`, etc.) for read paths; for create/replace it uses the
  kube-applier-aware helpers (`createKubeApplier`, `replaceKubeApplier`,
  &hellip;) that validate the caller-supplied partition key matches the
  *Desire's `Spec.ManagementCluster` rather than the resource ID's
  subscription ID.
- `KubeApplierGlobalListers` impls union the cluster- and node-pool-scoped
  resource types in a single cross-partition query, mirroring the
  ManagementClusterContent global lister.

### 3.3 Cosmos document wrapper

We continue to use `GenericDocument[T]` as the cosmos envelope, but the
type-switch in `convert_any.go` routes the three `*Desire` types through
`InternalToCosmosKubeApplier[T]`, which sets `partitionKey` from
`spec.managementCluster` instead of the resource ID's subscription ID.

This is the only convert-layer change. `CosmosToInternal` already handles
`GenericDocument[T]` via its default case.

### 3.4 Tests

- Unit tests for `ResourceParent.resourceID()` producing the exact format
  described in the readme (with and without nodepool).
- A round-trip create/get test using the mock client (proves the partition
  key carried in the cosmos envelope is the management cluster, not the
  subscription ID).
- A test that the cross-partition `ApplyDesires().List()` unions cluster-
  and node-pool-scoped resource types.

### 3.5 Mocks &mdash; `internal/databasetesting`

Mirror the production split:

- `MockDBClient` (existing) is unchanged. It does **not** know about
  kube-applier.
- `MockKubeApplierDBClient` (new) is a standalone in-memory implementation of
  `KubeApplierDBClient` with its own document store.
- A small `mockDocumentStore` interface lets the existing
  `mockResourceCRUD[T]` machinery be reused by the new mock without copying
  CRUD code.
- `NewMockKubeApplierDBClient()` and
  `NewMockKubeApplierDBClientWithResources(ctx, []any{...})` are the public
  test entry points, parallel to `NewMockDBClient*`.

## Risks / things to watch

- **Cross-container atomicity.** Cosmos `TransactionalBatch` is per-partition
  and per-container, so we cannot atomically write a `*Desire` and a
  `Resources`-container document in one shot. The backend must be designed
  to tolerate intermediate states. (This is consistent with current ARO-HCP
  behaviour.)
- **Indexing policy.** Confirm the new container's indexing policy (auto vs.
  custom) before we land it &mdash; cross-partition queries on
  `_resourceType` need an index.
- **Container creation pipeline.** The container itself is created by IaC
  (bicep). Adding the container to `dev-infrastructure/` is in scope for
  Doc 06 / 08, not for the database client PR itself.
- **Two clients in the backend process.** The backend will hold both a
  `DBClient` and a `KubeApplierDBClient`. They are intentionally independent;
  they are not joined under a parent struct. Any future cross-cutting
  concern (metrics, tracing) must be wired in twice.
