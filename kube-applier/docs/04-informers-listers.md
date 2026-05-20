# 04 &mdash; Informers, listers, and listertesting

## Why these live under `internal/database/`

The readme calls out three new packages:

- `internal/database/informers`
- `internal/database/listers`
- `internal/database/listertesting`

Today the equivalent code lives under `backend/pkg/{informers,listers,listertesting}`.
The reasoning for the new location:

- Both the **backend** (creator of `*Desire` documents) and the
  **kube-applier** (status-writer of `*Desire` documents) need shared informer
  and lister code. Having it under a backend-specific path would force
  kube-applier to reach across binary boundaries.
- The `internal/database/` location keeps it next to the CRUD/GlobalLister
  code that powers it, with no upward dependency on either binary.

We are **not** moving the existing backend informers/listers in this work.
The new packages are additive and only contain the `*Desire` plumbing.

## Reference patterns to mirror

- Informer factory pattern with cosmos-backed `ListWatch`:
  `backend/pkg/informers/informers.go:68-179`
- Index-key extraction from `CosmosMetadataAccessor`:
  `backend/pkg/informers/informers.go:600-615`
- Lister with `Get(...)` + `ListFor*(...)` + index lookups:
  `backend/pkg/listers/cluster_lister.go`
- Slice-backed fake lister:
  `backend/pkg/listertesting/slice_listers.go:27-65`
- DB-backed fake lister:
  `backend/pkg/listertesting/db_listers.go:27-52`

## Work items

### 4.1 `internal/database/listers`

Create one file per `*Desire`:

```
internal/database/listers/
  types.go                 // index constants, helpers (mirror backend/pkg/listers/types.go)
  apply_desire_lister.go
  delete_desire_lister.go
  read_desire_lister.go
```

Each lister exposes a focused interface. For `ApplyDesire`:

```go
type ApplyDesireLister interface {
    List(ctx context.Context) ([]*kubeapplier.ApplyDesire, error)
    Get(ctx context.Context, parent ResourceKey, name string) (*kubeapplier.ApplyDesire, error)
    ListForManagementCluster(ctx context.Context, mgmtCluster *azcorearm.ResourceID) ([]*kubeapplier.ApplyDesire, error)
    ListForCluster(ctx context.Context, sub, rg, cluster string) ([]*kubeapplier.ApplyDesire, error)
    ListForNodePool(ctx context.Context, sub, rg, cluster, np string) ([]*kubeapplier.ApplyDesire, error)
}
```

Indexers needed:

- `ByManagementCluster` &mdash; the only index used by the kube-applier
  itself.
- `ByCluster` &mdash; used by the backend to find all desires for a given
  HCP cluster (e.g. for cleanup on delete).
- `ByNodePool` &mdash; same, scoped to a nodepool.

Helper functions (`getByKey`, `listFromIndex`) are copy-paste from
`backend/pkg/listers/types.go:47-95`.

### 4.2 `internal/database/informers`

Create:

```
internal/database/informers/
  types.go                 // KubeApplierInformers interface + factory
  apply_desire_informer.go
  delete_desire_informer.go
  read_desire_informer.go
```

The factory mirrors `backend/pkg/informers/types.go:NewBackendInformers`:

```go
type KubeApplierInformers interface {
    ApplyDesires() (cache.SharedIndexInformer, listers.ApplyDesireLister)
    DeleteDesires() (cache.SharedIndexInformer, listers.DeleteDesireLister)
    ReadDesires() (cache.SharedIndexInformer, listers.ReadDesireLister)

    RunWithContext(ctx context.Context)
}

func NewKubeApplierInformers(ctx context.Context, gl database.KubeApplierListers) KubeApplierInformers
func NewKubeApplierInformersWithRelistDuration(ctx context.Context, gl database.KubeApplierListers, relistDuration time.Duration) KubeApplierInformers
```

Each per-type informer is the standard `cache.ListWatch` &rarr;
`SharedIndexInformer` wiring &mdash; copy `NewSubscriptionInformerWithRelistDuration`
in `backend/pkg/informers/informers.go:68-109` and adjust types. Pay
attention to:

- `ResyncPeriod`: 1h to match the existing convention.
- `ExpiringWatcher` with a relist duration of 30s for the kube-applier loop
  &mdash; the backend writes desires sparingly so 30s relists are cheap.
- Indexers must be registered up front:
  `cache.Indexers{listers.ByManagementCluster: ..., listers.ByCluster: ..., listers.ByNodePool: ...}`.

### 4.3 Per-management-cluster scoping comes from the container, not the lister

The kube-applier process should only ever care about desires for *its*
management cluster. In the per-MC container model that is automatic: the pod
holds a `KubeApplierDBClient` bound to its own container, and
`KubeApplierDBClient.Listers()` only returns documents from that container.
There is no shared container to filter against — credential isolation and
data isolation are the same thing.

The backend uses one informer per `KubeApplierDBClient` it obtains from
`KubeApplierDBClients.For(rid)`. Cross-MC iteration in backend controllers
walks `ManagementClusterResourceIDs()` and constructs a per-MC informer
(or, for one-shot sweeps, calls `UntypedCRUD` directly) as needed.

### 4.4 `internal/database/listertesting`

Mirror `backend/pkg/listertesting/`:

```
internal/database/listertesting/
  helpers.go
  slice_listers.go    // SliceApplyDesireLister, SliceDeleteDesireLister, SliceReadDesireLister
  db_listers.go       // DBApplyDesireLister wrapping a database.DBClient
```

Tests for these listers go alongside (`*_test.go`) and assert on the same
contract (`Get` returns NotFound when absent, `ListForCluster` filters
correctly, etc.). Copy `backend/pkg/listertesting/slice_listers_test.go` for
the structure.

## Acceptance for this layer

- Informers & listers compile with both cosmos-backed and slice-backed
  fakes.
- Round-trip: insert a `*Desire` via `MockDBClient`, see it appear in the
  informer's lister within one resync cycle (covered by an informer-level
  unit test).
- The lister's `ListForManagementCluster` returns only desires for the
  identified management cluster's partition.
- No code in `backend/` or `kube-applier/` depends on this package yet
  (still PR-shippable in isolation).
