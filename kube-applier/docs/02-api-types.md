# 02 &mdash; API package work (`internal/api/kubeapplier`)

## Current state

The package already exists with three minimal types:

- `internal/api/kubeapplier/types_apply_desire.go` &mdash; `ApplyDesire`
- `internal/api/kubeapplier/types_delete_desire.go` &mdash; `DeleteDesire`
- `internal/api/kubeapplier/types_read_desire.go` &mdash; `ReadDesire` + `ResourceReference`

Each embeds `api.CosmosMetadata` and exposes a `Spec` and `Status`. The
`Status.Conditions` is `[]metav1.Condition`.

Reference patterns to follow:

- Deepcopy markers: `internal/api/types_management_cluster_content.go:24` and
  package-level `internal/api/doc.go:15`.
- `CosmosMetadata` embedding & accessors:
  `internal/api/arm/types_cosmosdata.go:30-90`.
- `ResourceType` constants: `internal/api/types_cluster.go` and similar.

## Work items

### 2.1 Generate deepcopy

Add the following marker above each of `ApplyDesire`, `DeleteDesire`, and
`ReadDesire`:

```go
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
```

Add a `doc.go` at the package root:

```go
// +k8s:deepcopy-gen=package
package kubeapplier
```

Then run the standard generator (look at `make generate-mocks` /
`make generate` &mdash; the existing
`internal/api/zz_generated.deepcopy.go` was produced by a `make` target;
extend it to include this subpackage).

Acceptance:

- `internal/api/kubeapplier/zz_generated.deepcopy.go` exists.
- Each `*Desire` implements `runtime.Object` (`DeepCopyObject`,
  `GetObjectKind` &mdash; we may also need to embed `metav1.TypeMeta` for the
  latter; see 2.3).

### 2.2 Register `ResourceType` constants

The existing CRUD layer keys lookups by an `azcorearm.ResourceType` (see
`crud_nested_resource.go:30-62` &mdash; the third argument).

Add to a new `internal/api/kubeapplier/resource_types.go`:

```go
package kubeapplier

import azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

var (
    ApplyDesireResourceType  = azcorearm.NewResourceType(api.ProviderNamespace, "applydesires")
    DeleteDesireResourceType = azcorearm.NewResourceType(api.ProviderNamespace, "deletedesires")
    ReadDesireResourceType   = azcorearm.NewResourceType(api.ProviderNamespace, "readdesires")
)
```

(The exact provider-namespace constant lives near
`api.ClusterResourceType`.)

These are then used in:

- the CRUD constructors (Doc 03)
- the GlobalLister cross-partition queries (Doc 03)
- index-key helpers in the lister package (Doc 04)

### 2.3 Object metadata for runtime.Object

To satisfy `runtime.Object`, the types need a `GetObjectKind()` method or an
embedded `TypeMeta`. The simplest path that matches existing repo patterns is
to add `metav1.TypeMeta` embeds:

```go
type ApplyDesire struct {
    metav1.TypeMeta    `json:",inline"`
    api.CosmosMetadata `json:"cosmosMetadata"`
    Spec   ApplyDesireSpec   `json:"spec"`
    Status ApplyDesireStatus `json:"status"`
}
```

Verify against the existing embedded-vs-not pattern in
`internal/api/types_management_cluster_content.go` &mdash; mirror whichever
shape is used there.

### 2.4 Convenience helpers

Add a small `conditions.go` next to the types:

```go
const (
    ConditionSuccessful = "Successful"
    ConditionDegraded   = "Degraded"
)

const (
    ReasonKubeAPIError   = "KubeAPIError"
    ReasonPreCheckFailed = "PreCheckFailed"
    ReasonWaitingForDel  = "WaitingForDeletion"
    ReasonNoErrors       = "NoErrors"
    ReasonFailed         = "Failed"
)
```

These constants are the contract between the controllers and any test
harness; they MUST be named identifiers (not string literals) wherever
referenced by tests.

### 2.5 Resource ID builders

Mirror the existing `api.ToClusterResourceIDString` helpers
(`internal/api/types_cluster.go`). Add:

```go
// internal/api/kubeapplier/resource_ids.go
func ToApplyDesireResourceIDString(sub, rg, cluster, name string) string
func ToApplyDesireUnderNodePoolResourceIDString(sub, rg, cluster, np, name string) string
// ... and DeleteDesire / ReadDesire variants
func ParseDesireResourceID(id string) (DesireKey, error)
```

These are the canonical way to build the `*Desire` resource IDs and to
extract index keys (see Doc 04 for `ByCluster` / `ByNodePool` indexers).

## Acceptance for this layer

- `go build ./internal/api/...` passes.
- Generated deepcopy compiles and is committed.
- Hand-written unit tests in `internal/api/kubeapplier/*_test.go` cover:
  - Round-trip JSON for each `*Desire` (mirror existing tests on
    `HCPOpenShiftCluster`).
  - Resource-ID parse/format symmetry.
- No code outside `internal/api/kubeapplier` and `internal/api` itself depends
  on this package yet (so this layer can ship in its own PR).
