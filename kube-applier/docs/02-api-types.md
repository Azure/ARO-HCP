# 02 &mdash; API package work (`internal/api/kubeapplier`)

## Current state

The package already exists with two minimal types:

- `internal/api/kubeapplier/types_apply_desire.go` &mdash; `ApplyDesire`
- `internal/api/kubeapplier/types_read_desire.go` &mdash; `ReadDesire`
- `internal/api/kubeapplier/types_resource_reference.go` &mdash; `ResourceReference`

`ApplyDesire` uses a discriminated union via `ApplyDesireSpec.Type`
(`ServerSideApply` | `Delete`) to handle both server-side-apply and delete
operations. Each type embeds `api.CosmosMetadata` and exposes a `Spec` and
`Status`. The `Status.Conditions` is `[]metav1.Condition`.

Reference patterns to follow:

- Deepcopy markers: `internal/api/types_management_cluster_content.go:24` and
  package-level `internal/api/doc.go:15`.
- `CosmosMetadata` embedding & accessors:
  `internal/api/arm/types_cosmosdata.go:30-90`.
- `ResourceType` constants: `internal/api/types_cluster.go` and similar.

## Work items

### 2.1 Generate deepcopy

Add the following marker above each of `ApplyDesire`, `ReadDesire`, and
`ServerSideApplyConfig`:

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
- Each of `ApplyDesire` and `ReadDesire` implements `runtime.Object`
  (`DeepCopyObject`, `GetObjectKind` &mdash; we may also need to embed
  `metav1.TypeMeta` for the latter; see 2.3).

### 2.2 Register `ResourceType` constants

The existing CRUD layer keys lookups by an `azcorearm.ResourceType` (see
`crud_nested_resource.go:30-62` &mdash; the third argument).

Add to a new `internal/api/kubeapplier/resource_types.go`:

```go
package kubeapplier

import azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

var (
    ApplyDesireResourceType = azcorearm.NewResourceType(api.ProviderNamespace, "applydesires")
    ReadDesireResourceType  = azcorearm.NewResourceType(api.ProviderNamespace, "readdesires")
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
// internal/api/kubeapplier/types_cosmosdata.go
func ToClusterScopedApplyDesireResourceIDString(sub, rg, cluster, name string) string
func ToNodePoolScopedApplyDesireResourceIDString(sub, rg, cluster, np, name string) string
// ... and ReadDesire variants
// (there is no DeleteDesire type; deletion is an ApplyDesire with Spec.Type=Delete)
```

These are the canonical way to build the `*Desire` resource IDs and to
extract index keys (see Doc 04 for `ByCluster` / `ByNodePool` indexers).

### 2.6 Credential-scoped desires (SystemAdminCredentialRequest & SystemAdminCredentialRevocation)

Desire resources can have `SystemAdminCredentialRequest` **and**
`SystemAdminCredentialRevocation` as parent resources, in addition to clusters
and node pools. This enables proper nesting so that credential-related desires
are scoped under the credential request or revocation they belong to, and the
resource hierarchy matches the resource that owns them.

**Resource types** (in `registry.go`):

```go
CredentialRequestScopedApplyDesireResourceType = nestedResourceType(ClusterResourceTypeName, SystemAdminCredentialRequestResourceTypeName, ApplyDesireResourceTypeName)
CredentialRequestScopedReadDesireResourceType  = nestedResourceType(ClusterResourceTypeName, SystemAdminCredentialRequestResourceTypeName, ReadDesireResourceTypeName)

RevocationScopedApplyDesireResourceType = nestedResourceType(ClusterResourceTypeName, SystemAdminCredentialRevocationResourceTypeName, ApplyDesireResourceTypeName)
RevocationScopedReadDesireResourceType  = nestedResourceType(ClusterResourceTypeName, SystemAdminCredentialRevocationResourceTypeName, ReadDesireResourceTypeName)
```

> There is no `DeleteDesire` resource type. Deletion is modeled as an
> `ApplyDesire` whose `Spec.Type` is `Delete` (the `ApplyDesireSpec.Type`
> discriminated union described under "Current state" above), so
> credential teardown reuses the ApplyDesire type rather than a
> distinct DeleteDesire.

**Resource ID builders** (in `types_cosmosdata.go`):

```go
func ToCredentialRequestScopedApplyDesireResourceIDString(sub, rg, cluster, credReq, name string) string
func ToCredentialRequestScopedReadDesireResourceIDString(sub, rg, cluster, credReq, name string) string

func ToRevocationScopedApplyDesireResourceIDString(sub, rg, cluster, revocation, name string) string
func ToRevocationScopedReadDesireResourceIDString(sub, rg, cluster, revocation, name string) string
```

These produce resource IDs of the form:

```
/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.RedHatOpenShift/
  hcpOpenShiftClusters/{cluster}/systemAdminCredentialRequests/{cred}/
  applyDesires/{name}

/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.RedHatOpenShift/
  hcpOpenShiftClusters/{cluster}/systemAdminCredentialRevocations/{revocation}/
  applyDesires/{name}
```

**Cosmos CRUD** (in `internal/database/kube_applier_client.go`): the
`KubeApplierDBClient` interface exposes per-parent CRUD accessors alongside the
existing cluster/node-pool ones:

```go
ApplyDesiresForCredentialRequest(sub, rg, cluster, credReq string) (ResourceCRUD[...], error)
ReadDesiresForCredentialRequest(sub, rg, cluster, credReq string) (ResourceCRUD[...], error)
ApplyDesiresForRevocation(sub, rg, cluster, revocation string) (ResourceCRUD[...], error)
ReadDesiresForRevocation(sub, rg, cluster, revocation string) (ResourceCRUD[...], error)
```

The per-management-cluster listers and change-feed informers list all four
scopes (cluster, node pool, credential request, revocation) so nested desires
are indexed and cleanup via `ListForCluster` still finds every desire under a
cluster regardless of its parent.

**Controllers**: the desires-creator nests a credential's CSR / CSRApproval /
RBAC / ReadDesire under its `SystemAdminCredentialRequest`, and the
revocation-desires controller nests the CRR / RBAC / ReadDesire under its
`SystemAdminCredentialRevocation`. The teardown controllers delete each
credential's or revocation's desires through the matching scoped CRUD.

**Rationale**: Nesting desires under `SystemAdminCredentialRequest` /
`SystemAdminCredentialRevocation` makes cleanup automatic when the parent
document is deleted, removes the need for an `OutstandingDesires` tracking
field, lets controllers fire when desires change via informer watches, and
makes it easy to find all desires for a specific credential request or
revocation.

## Acceptance for this layer

- `go build ./internal/api/...` passes.
- Generated deepcopy compiles and is committed.
- Hand-written unit tests in `internal/api/kubeapplier/*_test.go` cover:
  - Round-trip JSON for each `*Desire` (mirror existing tests on
    `HCPOpenShiftCluster`).
  - Resource-ID parse/format symmetry (including credential-request-scoped variants).
- No code outside `internal/api/kubeapplier` and `internal/api` itself depends
  on this package yet (so this layer can ship in its own PR).
