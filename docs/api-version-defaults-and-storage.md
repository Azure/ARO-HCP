# DDR: API Version Defaults and Cosmos Storage Consistency

**Status:** Accepted
**Date:** 2026-02-23
**Authors:** @sudobrendan
**Context:** Ephemeral OS Disk feature (adding `diskType` field to NodePool)

## Problem Statement

When a new API version introduces a field with a default value, pre-existing
documents in Cosmos DB do not contain that field. Both Frontend (FE) and
Backend (BE) read directly from Cosmos. Without a consistent defaulting
strategy, the system risks three failure modes:

1. **Incorrect API responses.** A GET on the new API version omits the new
   field or returns a zero value instead of the expected default. For
   example, a node pool created before `diskType` was added would have no
   `diskType` in Cosmos, and the API response would omit the field rather
   than showing the default `"Managed"`.
2. **Conversion errors.** Code that converts internal types to other
   representations may error on zero-valued fields. For example,
   `convertDiskTypeRPToCS` (called during FE node pool create/update)
   returns an error for an empty `DiskType` because the empty string is
   not a recognized enum value.
3. **Silent data divergence.** FE and BE can end up with different views
   of the same resource if their defaulting logic disagrees. This can
   happen in two ways: (a) both read from Cosmos but apply different
   defaults, or (b) FE sources a field from Cluster Service (which has
   its own defaulting in `convertDiskTypeCSToRP`) while BE sources the
   same field from Cosmos (which has storage defaults in
   `CosmosToInternal*`). If the CS→RP default and the storage default
   disagree, FE and BE see different values for the same resource.

## Prior Art

### Kubernetes

Kubernetes applies defaults via the versioning codec's
[`Decode` method][k8s-versioning]. The `Decode` method calls
[`Scheme.Default()`][k8s-scheme-default] on the decoded object at each
version boundary.

Defaulting functions are registered per API version for versioned
(external) types (e.g., [`SetDefaults_Deployment`][k8s-apps-defaults] in
`apps/v1`). The `Scheme.Default()` method dispatches by `reflect.TypeOf`,
calling the registered defaulter for the object's concrete type.

On the **write path** (HTTP request body → etcd):

1. Decode the request body into the **versioned** (external) type.
2. Call `Scheme.Default()` on the versioned type (applies version-specific
   defaults).
3. Convert from versioned type to the **internal** (hub) type.
4. Convert from internal type to the **storage version** for persistence.

On the **read path** (etcd → API response), etcd stores objects in a
**versioned encoding** (the storage version, e.g., `apps/v1` for
Deployments — not the internal type). The storage codec decodes from the
storage version, converts to the internal type, then converts to the
requested API version and applies versioned defaults via `Scheme.Default()`.

Defaults on read is safe in Kubernetes because the **kube-apiserver is the
single reader and writer** of etcd. There is no second component that could
read stale data.

Background migration ([StorageVersionMigrator][k8s-svm]) is an optional
optimization that re-encodes stored objects in the current storage version.
It is not required for correctness.

[k8s-versioning]: https://github.com/kubernetes/kubernetes/blob/60433d43cf0bb83a2ac7d5e767137b3d510026ec/staging/src/k8s.io/apimachinery/pkg/runtime/serializer/versioning/versioning.go#L170
[k8s-scheme-default]: https://github.com/kubernetes/kubernetes/blob/60433d43cf0bb83a2ac7d5e767137b3d510026ec/staging/src/k8s.io/apimachinery/pkg/runtime/scheme.go#L413
[k8s-apps-defaults]: https://github.com/kubernetes/kubernetes/blob/60433d43cf0bb83a2ac7d5e767137b3d510026ec/pkg/apis/apps/v1/defaults.go#L32-L67
[k8s-svm]: https://github.com/kubernetes-sigs/kube-storage-version-migrator

### ARO Classic

ARO Classic uses three layers:

1. **On write:** [`api.SetDefaults()`][aro-setdefaults] is called
   [before Cosmos write][aro-putorpatch-defaults] in the PUT/PATCH handler.
2. **On read:** The GET handler calls
   [`clusterEnricher.Enrich()`][aro-get-enrich], which invokes
   per-enricher [`SetDefaults` methods][aro-enricher-setdefaults] on each
   `ClusterEnricher` implementation in-memory before returning the
   response. Note: these are per-enricher interface methods that default
   enricher-specific fields, distinct from the top-level `api.SetDefaults`
   used on the write path.
3. **Background maintenance:** [`ensureDefaults()`][aro-ensuredefaults]
   calls `api.SetDefaults` via `PatchWithLease` during AdminUpdate
   operations, persisting defaults to Cosmos.

[aro-setdefaults]: https://github.com/Azure/ARO-RP/blob/f173264b1a723fdb9d4c1ddd907eb75c6bed9649/pkg/api/defaults.go#L10-L65
[aro-putorpatch-defaults]: https://github.com/Azure/ARO-RP/blob/f173264b1a723fdb9d4c1ddd907eb75c6bed9649/pkg/frontend/openshiftcluster_putorpatch.go#L348
[aro-get-enrich]: https://github.com/Azure/ARO-RP/blob/f173264b1a723fdb9d4c1ddd907eb75c6bed9649/pkg/frontend/openshiftcluster_get.go#L50
[aro-enricher-setdefaults]: https://github.com/Azure/ARO-RP/blob/f173264b1a723fdb9d4c1ddd907eb75c6bed9649/pkg/util/clusterdata/clusterdata.go#L134
[aro-ensuredefaults]: https://github.com/Azure/ARO-RP/blob/f173264b1a723fdb9d4c1ddd907eb75c6bed9649/pkg/cluster/defaults.go#L13-L26

### Key Difference in ARO-HCP

Compared to Kubernetes - our API spec is fundamentally different in regards to versioning which has impacts on read and write operations. ARM _requires_ cross-version compatibility. You can create a Resource in v1, and GET it in v2 (and you should see the expected and complete API surface returned to you). Similarly - you can create a Resource in v2, and GET that resource in v1 - effectively this means that the API versions record _what format your request/response is_ rather than determining _what the Resource is_ (ultimately - that's determined by whatever the most recent API spec includes - Azure expects complete backwards-compatibility across API versions).

In addition, ARO-HCP has **two independent Cosmos readers**: Frontend and Backend. This
means Kubernetes's "defaults on read in the API server" pattern is
insufficient — it would only cover FE, leaving BE exposed to "stale"/unpopulated data it reads directly from cosmos.

### Design inspiration from Kubernetes

Kubernetes ensures defaults are applied at every version boundary crossing
via `Scheme.Default()` on versioned types. ARO-HCP adapts this principle
for its multi-reader architecture:

| Kubernetes | ARO-HCP | Purpose |
|-----------|---------|---------|
| Versioned-type `Scheme.Default()` | `SetDefaultValues*()` in versioned conversion | Version-specific presentation defaults |
| *(no direct equivalent — kube-apiserver is the single reader)* | Storage defaults in `CosmosToInternal*()` | Cross-version invariant defaults for all readers |

The storage defaults layer in `CosmosToInternal*()` has no direct
Kubernetes parallel because Kubernetes does not need one — the
kube-apiserver is the only component reading from etcd. ARO-HCP
introduces this layer to solve the multi-reader consistency problem.

## Decision

### Where defaults are applied

Defaults for new fields are applied in the **Cosmos-to-internal conversion
layer** (`internal/database/convert_*.go`), which is the shared read path
for both FE and BE.

```
Cosmos bytes
  → CosmosToInternalNodePool()        ← apply storage defaults HERE
  → api.HCPOpenShiftClusterNodePool   (internal type, always has defaults)
  → used by FE (API conversion) and BE (controllers) consistently
```

This layer ensures that every consumer of the database client interface
reads documents with correct defaults, regardless of when the document
was originally written to Cosmos.

### How FE GET responses are assembled

FE GET and List handlers do not return Cosmos data directly. They use a
**CS-first merge** strategy:

1. Read from Cosmos → `CosmosToInternal*()` (storage defaults applied).
2. Read from Cluster Service (CS) → `mergeToInternal*()` converts the
   CS response to the internal type, then selectively overwrites a few
   fields from Cosmos (SystemData, Tags, ProvisioningState,
   ServiceProviderProperties).
3. The merged result is converted to the requested API version for the
   response.

Most operational fields in the API response — including `DiskType`,
`AutoRepair`, `Replicas`, `Version`, network settings, etc. — come from
**Cluster Service**, not Cosmos. Only ARM-managed metadata comes from
Cosmos.

This means storage defaults in `CosmosToInternal*()` are **harmlessly
overwritten** for CS-sourced fields on the FE GET path. Their primary
consumers are:

- **BE controllers**, which read from Cosmos and never call CS.
- **`MigrateCosmosOrDie`**, which reads from Cosmos, applies storage
  defaults, and writes back to persist them.
- **FE create/update paths**, where the Cosmos read provides the
  "old state" before the user's changes are overlaid.

If CS is unreachable, the FE GET fails entirely with an HTTP error —
there is no fallback to Cosmos-only data, and no partial response is
returned. This means storage defaults are never exposed in a "degraded"
FE response; the response simply does not exist.

### The `MigrateCosmosOrDie` interaction

`MigrateCosmosOrDie()` runs at FE startup and does a Get→Replace round-trip
on cluster, node pool, controller, and external auth documents in Cosmos.
(Subscriptions and operations are only read, not replaced, so they would
not be backfilled by this mechanism.) Once storage defaults are added to
`CosmosToInternal*()`, the Get leg of the round-trip will apply defaults,
and the subsequent Replace call will persist those defaults back to Cosmos.
No additional migration code is needed — the existing infrastructure
becomes a defaulting migration for free.

**Important:** This "free migration" property only emerges after storage
defaults are implemented in `CosmosToInternal*()`. Without that code
change, `MigrateCosmosOrDie` round-trips the exact same data with no
effect on new fields.

```
MigrateCosmosOrDie loop (after storage defaults are implemented):
  Get  → CosmosToInternal (applies defaults) → internal obj with defaults
  Replace → InternalToCosmos → Cosmos now has defaults persisted
```

After the next FE deploy, all documents in Cosmos will have the new field
populated. Between deploys, the read-time defaulting ensures correctness.

**Why this works without additional migration code:**

- The Get leg calls `CosmosToInternal*()`, which applies storage defaults
  on read. This ensures correctness even before the Replace persists them.
- The Replace leg calls `InternalToCosmos*()`, which persists the
  defaulted values back to Cosmos. This is an optimization that avoids
  re-applying defaults on every subsequent read.
- Both FE and BE benefit: FE from the persisted values (fewer defaults
  applied on read), BE from the read-time defaults (always correct
  regardless of when the document was last written).

### Layered summary

| Layer | Where | Primary consumer | Purpose | Required? |
|-------|-------|-----------------|---------|-----------|
| Storage defaults | `CosmosToInternal*()` | BE, `MigrateCosmosOrDie` | Every Cosmos reader gets correct data | **Yes** (must be implemented) |
| CS→RP defaults | `convertDiskTypeCSToRP()` etc. in `internal/ocm/convert.go` | FE GET/List | CS may return empty values for pre-existing resources; maps them to correct defaults | Already exists for `DiskType` |
| Eager migration | `MigrateCosmosOrDie` Get→Replace | All future readers | Persists defaults to Cosmos at startup | Automatic once storage defaults exist |
| API-version defaults | `SetDefaultValues*()` in versioned conversion | FE write path | Version-specific presentation defaults | Already exists |
| Write-path defaults | `SetDefaultValues*()` + `normalize*()` | FE write path | New documents always have defaults | Already exists |

**Consistency requirement:** Storage defaults and CS→RP defaults must
agree on the same value for each field. If they diverge, FE (which
sources operational fields from CS) and BE (which sources them from
Cosmos) will see different values for the same resource.

## When a Field Is Safe to Default on Read

The storage defaulting function must distinguish "field was never set" from
"field was explicitly set to its zero value." Whether this is possible
depends on the field's type and JSON serialization tag in the **internal
type** (the type stored in `InternalState.InternalAPI` in Cosmos).

### Never default: ARM-managed fields

These fields are managed by the ARM service contract and restored from
`ResourceDocument` during `CosmosToInternal*()`. They must never have
storage defaults applied. The current architecture provides structural
protection: these fields are stored on `ResourceDocument` (not in
`InternalState.InternalAPI`), and `CosmosToInternal*()` overwrites them
from `ResourceDocument` regardless of what the `InternalAPI` blob contains.

| Field | Why excluded |
|-------|-------------|
| `provisioningState` | Read-only. Managed by the async operation state machine. Restored from `ResourceDocument.ProvisioningState`. |
| `systemData` | Read-only. Managed by ARM. Restored from `ResourceDocument.SystemData`. |
| `id`, `name`, `type` | Read-only top-level properties from TrackedResource/ProxyResource. Restored from `ResourceDocument.ResourceID`. |
| `tags` | Mutable but stored on `ResourceDocument`, not `InternalAPI`. |

Storage defaulting functions should only default **customer-settable
properties** within `InternalState.InternalAPI`.

### Safe: zero value is never valid user input

These fields can be defaulted unconditionally when the zero value is
encountered.

| Type pattern | Example | Why safe |
|-------------|---------|----------|
| String enum + `omitempty` | `DiskType OsDiskType` | `""` is not a valid enum member |
| String enum + `omitempty` | `DiskStorageAccountType` | `""` is not a valid enum member |

For these fields, the defaulting function is straightforward:

```go
if np.Properties.Platform.OSDisk.DiskType == "" {
    np.Properties.Platform.OSDisk.DiskType = api.OsDiskTypeManaged
}
```

### Unsafe: zero value IS valid user input

These fields cannot be safely defaulted on read without a type change,
because the zero value is indistinguishable from "never set" after a
Cosmos round-trip.

#### NodePool fields (`internal/api/types_nodepool.go`)

| Type | Field | Why unsafe |
|------|-------|------------|
| `bool` + `omitempty` | `AutoRepair` | `false` is valid. `omitempty` omits `false` from JSON. After deserialize, `false` == "never set". |
| `int32` + `omitempty` | `Replicas` | `0` may be valid (e.g., with autoscaling). `omitempty` omits `0`. |
| `int32` + `omitempty` | `AutoScaling.Min` | Same structural problem as `Replicas`. |
| `int32` + `omitempty` | `AutoScaling.Max` | Same structural problem as `Replicas`. |

#### Cluster fields (`internal/api/types_cluster.go`)

| Type | Field | Why unsafe |
|------|-------|------------|
| `int32` + `omitempty` | `NodeDrainTimeoutMinutes` | `0` is valid (no timeout). |
| `int32` + `omitempty` | `HostPrefix` | `0` not semantically valid (validation requires >0), but structurally unsafe. |
| `int32` + `omitempty` | `MaxNodesTotal` | `0` may be valid. |
| `int32` + `omitempty` | `MaxPodGracePeriodSeconds` | `0` is valid (immediate eviction). |
| `int32` + `omitempty` | `MaxNodeProvisionTimeSeconds` | `0` may be valid. |
| `int32` + `omitempty` | `PodPriorityThreshold` | `0` is meaningful (negative values are also valid). |

All 10 fields use `PtrOrNil()` in the versioned internal→external
conversion, which compounds the problem: `PtrOrNil` converts zero values
to `nil`, causing the field to be omitted from API responses.

#### Impact on PUT (GET-then-PUT round-trip data loss)

A GET-then-PUT round-trip on any of these fields with a zero value
silently replaces the customer's explicit choice with the default:

```
Customer sets autoRepair=false via PUT
  → internal type: AutoRepair = false
  → GET response: PtrOrNil(false) → nil → field OMITTED from JSON
  → Customer does GET → PUT with the same body
  → SetDefaultValues: AutoRepair == nil → ptr.To(true)
  → Customer's explicit false silently becomes true
```

This is a real data loss bug on the PUT path. `SetDefaultValues` is
called on both the create and PUT/replace paths, so any nil field
triggers re-defaulting.

#### Impact on PATCH (no data loss today)

The PATCH path does **not** call `SetDefaultValues`. It uses RFC 7396
JSON Merge Patch via `ApplyRequestBody`:

1. Convert existing internal state to external via
   `NewHCPOpenShiftClusterNodePool` (uses `PtrOrNil`).
2. Serialize to JSON (the "base document" — zero-valued fields are
   absent due to `PtrOrNil`).
3. Apply the PATCH body via JSON Merge Patch.
4. Deserialize back to the external type.
5. Convert to internal via `ConvertToInternal`.

Because `SetDefaultValues` is not called in step 4, nil fields stay
nil and convert to Go's zero value in step 5. For these fields, the
zero value is exactly the value that `PtrOrNil` erased in step 1, so
the round-trip is correct:

```
AutoRepair = false
  → PtrOrNil(false) → nil → absent in base JSON
  → merge patch (no change) → absent
  → unmarshal → nil
  → ConvertToInternal skips nil → AutoRepair = false (Go zero value)
  → correct: false round-trips through nil → absent → zero value
```

This works because Go's zero-value initialization guarantees the
round-trip is lossless for exactly the values `PtrOrNil` erases. The
base document is lossy (zero-valued fields are missing), but the end
result is correct for all current fields.

However, the PATCH path has a subtle gap: what should happen when a
customer sends `null` for a field that has a server default?

RFC 7396 (JSON Merge Patch) specifies that `null` means "remove the
member." It does not specify what "remove" means for a field that
cannot be absent — it simply says the member is removed from the
target. The ARM API guidelines say the service should either delete
the field or return `400-BadRequest` for undeletable fields. Neither
RFC 7396 nor ARM prescribes "reset to server default" as the expected
behavior.

Currently, `null` in a PATCH body produces Go's zero value (e.g.,
`false` for `bool`) after deserialization. Whether this is correct
depends on whether we define "remove" as "reset to zero value,"
"reset to server default," or "reject the operation."

#### `PtrOrNil` vs `Ptr` and PATCH null semantics (Open Question)

The correct PATCH null behavior is unresolved and has several options:

**Option A: `PtrOrNil` → `Ptr` + `SetDefaultValues` on PATCH.**
Use `Ptr` in `NewHCPOpenShiftCluster*()` so the base document for the
merge patch is lossless. Then call `SetDefaultValues` after the merge
so that only fields the customer explicitly set to `null` get
re-defaulted. This interprets `null` as "reset to server default."

| PATCH body | Base (`Ptr`) | After merge | After `SetDefaultValues` | Result |
|-----------|-------------|-------------|------------------------|--------|
| `{}` (absent) | `"autoRepair": false` | `"autoRepair": false` | `&false` (non-nil, skip) | `false` — unchanged |
| `"autoRepair": null` | `"autoRepair": false` | absent | `nil` → `ptr.To(true)` | `true` — reset to default |
| `"autoRepair": true` | `"autoRepair": false` | `"autoRepair": true` | `&true` (non-nil, skip) | `true` — explicit value |

**Why `SetDefaultValues` on PATCH is safe only with `Ptr`:** With
`PtrOrNil`, the base document is lossy — zero-valued fields are already
absent. `SetDefaultValues` cannot distinguish "customer sent null"
from "field was absent because `PtrOrNil` erased it," so a no-op PATCH
on a resource with `autoRepair=false` would incorrectly reset it to
`true`. With `Ptr`, the base is lossless: absent after merge means the
customer explicitly removed it.

**Option B: Reject null on non-nullable fields.** Return
`400-BadRequest` when the customer sends `null` for a field that cannot
be absent. This is explicitly allowed by ARM guidelines and avoids
ambiguity about what "remove" means. Requires validation logic on the
PATCH path.

**Option C: Current behavior (null → zero value).** Accept `null` and
let it produce Go's zero value. This is what happens today. For fields
like `AutoRepair`, this means `null` → `false`, which is a valid value
but not the server default. The customer gets a deterministic result,
but it may not match their intent.

Each option involves tradeoffs around ARM compliance, customer
expectations, and implementation complexity. This decision is deferred
and out of scope for the storage defaults work.

**Existing risk: `AutoRepair`.** The code comments on the `PtrOrNil`
usage state "Keep PtrOrNil for AutoRepair since default is true —
omitting false allows client to use default," suggesting this was an
intentional design choice rather than an oversight. Regardless of intent,
the behavior creates a PUT round-trip data loss risk. The PATCH path is
not affected today (because `SetDefaultValues` is not called), but the
base document is still lossy. The ephemeral disk `DiskType` field does
not have this problem because empty string is never a valid enum value.
Addressing these unsafe fields is out of scope for the ephemeral disk
work.

#### Current PATCH null behavior

Sending `null` for a field in a PATCH body currently produces Go's zero
value after JSON deserialization. For example,
`PATCH {"properties": {"autoRepair": null}}` results in
`AutoRepair = false` (the Go zero value for `bool`), not `true` (the
server default). This differs from omitting the field entirely (which
preserves the existing value through the merge patch base document).

This behavior is deterministic but may not match customer intent — the
customer likely expects "reset to default" rather than "set to false."
The correct behavior is an open question documented in the `PtrOrNil`
vs `Ptr` section above.

#### Root cause: `omitempty` on value types in Cosmos storage

Separate from the `PtrOrNil` issue in API responses, the `omitempty`
tag on value types in the internal type causes data loss in Cosmos:

```
Customer sets autoRepair=false
  → internal type: AutoRepair = false
  → json.Marshal with omitempty: field OMITTED
  → Cosmos: no "autoRepair" field
  → json.Unmarshal: AutoRepair = false (zero value)
  → indistinguishable from "document predates this field"
```

Defaulting `false → true` on read would silently corrupt an intentional
customer choice. This is a storage-layer problem that affects both
read-path defaulting and `MigrateCosmosOrDie` backfill.

### Making unsafe fields safe

There are two independent problems to fix, each with its own solution:

**Problem 1: API response omits zero values (`PtrOrNil` in versioned
conversion).** This causes GET-then-PUT data loss.

| Approach | Change | Effect |
|----------|--------|--------|
| `PtrOrNil` → `Ptr` | Use `Ptr` in `NewHCPOpenShiftCluster*()` | GET response includes `"autoRepair": false"` explicitly. PUT round-trip preserves the value. PATCH base document becomes lossless. |

The codebase already has this pattern: `EnableEncryptionAtHost` uses
`Ptr` (not `PtrOrNil`) with the comment "Use Ptr (not PtrOrNil) to
ensure boolean is always present in JSON response, even when false."

**Problem 2: Cosmos storage omits zero values (`omitempty` on value
types in the internal type).** This prevents safe read-path defaulting
and `MigrateCosmosOrDie` backfill.

| Approach | Change | Tradeoff |
|----------|--------|----------|
| Pointer types | `AutoRepair *bool` | `nil` = never set, `&false` = explicit. More nil checks in code. This is what Kubernetes versioned types do. Solves both problems at once. |
| Remove `omitempty` | `AutoRepair bool \`json:"autoRepair"\`` | `false` explicitly stored in JSON. Cosmos documents always have every field. Larger documents. |

The codebase already has precedent for the "remove `omitempty`" approach:
`EnableEncryptionAtHost bool \`json:"enableEncryptionAtHost"\`` uses `bool`
without `omitempty`, which means `false` is explicitly serialized to JSON
and survives Cosmos round-trips.

Either approach to Problem 2 requires a migration: existing documents
with omitted fields must be updated to contain the explicit value.
`MigrateCosmosOrDie` handles this naturally via its Get→Replace sweep.

**Fixing Problem 1 (`PtrOrNil` → `Ptr`) can be done independently and
immediately.** It has no migration requirement and fixes GET-then-PUT
data loss. Problem 2 (Cosmos storage) is a deeper change that requires
migration and is a prerequisite for safe read-path defaulting of these
fields.

**Recommendation:** When adding a new bool or int field to the internal
type, prefer pointer types if the zero value is a valid user choice. This
solves both problems from the start. For existing fields, start by
changing `PtrOrNil` to `Ptr` in the versioned conversion (no migration
needed), then plan the Cosmos storage fix separately.

## Adding a New Field: Checklist

When adding a new field to the API across versions:

### 1. Internal type (`internal/api/types_*.go`)

- [ ] Add the field to the internal type.
- [ ] Choose the type carefully:
  - String enum → value type + `omitempty` is fine (safe to default).
  - Bool/int where zero is valid → use `*bool`/`*int32` or remove
    `omitempty` so the explicit value survives Cosmos round-trip.
- [ ] Add the field with its default to `NewDefault*()` constructor.

### 2. Versioned conversion (`internal/api/v*/methods.go`)

- [ ] Add `SetDefaultValues*()` entry: `if field == nil { field = default }`.
- [ ] Add `ConvertToInternal` (normalize) logic.
- [ ] Add `NewHCPOpenShiftCluster*()` (internal→external) mapping.
- [ ] For older API versions that don't expose the field: force the default
  in `normalize*()` (e.g., v1's `normalizeOSDiskProfile` sets
  `DiskType = Managed`).

### 3. Storage defaults (`internal/database/convert_*.go`)

- [ ] Create or extend a storage defaulting function (e.g.,
  `applyNodePoolStorageDefaults()`) called from the `CosmosToInternal*`
  function for the relevant resource type. This function does not exist
  yet — it is new code introduced by this DDR's decision.
- [ ] Only default if the field is safe (zero value is never valid), OR
  if the internal type uses pointers (nil = unset).
- [ ] Never add storage defaults for ARM-managed fields
  (`provisioningState`, `systemData`, `id`/`name`/`type`, `tags`) —
  these are restored from `ResourceDocument`, not `InternalAPI`.

### 4. Tests

- [ ] Add version compliance test scenarios
  (`test-integration/frontend/artifacts/VersionCompliance/`):
  - Scenario with default value: create via v1, GET via v2 shows default.
  - Scenario with explicit value: create via v2 with non-default, GET via
    v1 omits field, GET via v2 shows explicit value.
- [ ] Add or update Cosmos compare fixtures for the new field.
- [ ] Add pre-existing data scenario: load old Cosmos document without the
  field, verify GET via both API versions returns correct response.

### 5. OCM conversion (`internal/ocm/convert.go`)

- [ ] **RP→CS** (FE create/update, writing to CS): On the create path,
  API-version defaults from `SetDefaultValues*()` guarantee non-empty
  values — no Cosmos read occurs. The conversion function should error
  on unexpected zero values to catch bugs.
- [ ] **CS→RP** (reading resource state from CS): Cluster Service may
  return empty values for pre-existing resources that predate the new
  field. The conversion function must map missing/empty values to the
  correct default (e.g., `convertDiskTypeCSToRP` maps `""` to
  `Managed`).
- [ ] **Consistency:** The CS→RP default **must match** the storage
  default in `CosmosToInternal*()`. FE GET responses source most
  operational fields from CS (via `mergeToInternal*`), while BE reads
  from Cosmos. If these defaults disagree, FE and BE will see different
  values for the same resource.

### 6. Validation (`internal/validation/`)

- [ ] Add validation for the new field's allowed values.
- [ ] Consider immutability constraints (can the field change after
  creation?).

## Rolling Deployment Considerations

During a rolling deployment, old FE/BE code and new FE/BE code run
simultaneously. The storage defaulting in `CosmosToInternal` ensures that
new code always reads correct defaults. Old code will read documents
written by new code and encounter the new field — it must ignore unknown
fields gracefully. Go's standard `json.Unmarshal` silently discards
unknown JSON fields by default, so old code that lacks the new struct
field will simply drop it on read.

**Note on re-serialization:** When old code reads a document containing
a new field and then writes it back to Cosmos, the new field will be
silently dropped because the old struct has no field to hold it. This is
a pre-existing characteristic of the `MigrateCosmosOrDie` design and
rolling deployments in general, and is not introduced or worsened by
storage defaults. Once all instances are running the new code, the next
`MigrateCosmosOrDie` sweep will restore any dropped fields.

### Deploy ordering

The read-time defaulting in `CosmosToInternal` makes deploy ordering
irrelevant for **correctness** — regardless of whether FE or BE deploys
first, any instance running the new code will read correct defaults from
Cosmos.

The eager migration of all documents only happens when FE deploys (via
`MigrateCosmosOrDie`). BE has no equivalent startup sweep. If BE deploys
before FE, there is a window where not all documents have been backfilled
in Cosmos. This window is harmless because BE's read-time defaulting in
`CosmosToInternal` covers it. Once the first new FE instance starts and
completes its `MigrateCosmosOrDie` sweep, all documents in Cosmos will
have the new field persisted.

### Concurrent FE startup

When multiple FE pods start simultaneously (as happens in a Kubernetes
deployment), they will all run `MigrateCosmosOrDie` concurrently. The
Replace calls for clusters and node pools do not use ETag-based
conditional writes. The custom `CosmosToInternalCluster` and
`CosmosToInternalNodePool` functions do not propagate the Cosmos ETag
to the internal object (unlike `CosmosToInternalController` and the
generic conversion path, which do call `SetEtag`). Additionally, the
cluster and node pool types' `GetCosmosData()` methods construct a new
`CosmosMetadata` that never includes an ETag. As a result, the
`replace` function's `IfMatchEtag` check finds no ETag and performs an
unconditional write. Concurrent Replace calls are therefore
last-writer-wins.
Since all instances apply the same defaults from the same code version,
the result is idempotent — every writer produces the same output
document. This makes concurrent startup safe without any additional
coordination. Races between the migration sweep and concurrent request
handlers are a pre-existing concern with the `MigrateCosmosOrDie` design
(which does not use conditional writes) and are not introduced or
worsened by the addition of storage defaults.

Documents created during the migration sweep are safe because they go
through write-path defaulting and will already have the new field set.

### Old FE instances

Old FE instances still running will read documents updated by new
instances and encounter the new field. Go's standard `json.Unmarshal`
silently discards unknown JSON fields, so old code handles this
gracefully.

## Applied Fixes

### AutoRepair Response Shape Change (v20251223preview)

The `AutoRepair` field used `PtrOrNil` in the internal-to-external
conversion, which omitted `autoRepair: false` from GET responses. This
caused GET-then-PUT data loss: a customer who explicitly set
`autoRepair=false` would GET a response without the field, PUT the same
body back, and `SetDefaultValues` would reset it to `true`.

**Fix**: Changed `PtrOrNil` to `Ptr` in v20251223preview only.

| API Version | Conversion | GET response for `autoRepair=false` |
|-------------|-----------|-------------------------------------|
| v20240610preview (shipped) | `PtrOrNil` | Field omitted from JSON |
| v20251223preview (unreleased) | `Ptr` | `"autoRepair": false` present |

This fix is gated behind the unreleased API version to avoid breaking
existing customer automation on v20240610preview. Customers must use
v20251223preview or later to get lossless PUT round-trips for
`AutoRepair`.

The codebase already has this pattern: `EnableEncryptionAtHost` uses
`Ptr` (not `PtrOrNil`) with the comment "Use Ptr (not PtrOrNil) to
ensure boolean is always present in JSON response, even when false."

## Future Considerations

### Append-only storage defaulting functions

Storage defaulting functions in `CosmosToInternal*()` should be treated
as **append-only**: avoid removing old defaulting logic until all
documents in Cosmos have been migrated past the version that introduced
the field. Each defaulting rule handles a specific historical document
shape, and documents from any era may still exist in Cosmos. After
`MigrateCosmosOrDie` has verifiably backfilled all documents, removal is
technically safe, but keeping the rule is cheap insurance against edge
cases like database restores or partial migration failures.

### Type-change migrations

If a field's type needs to change (e.g., migrating `AutoRepair bool` to
`AutoRepair *bool`), Go's `json.Unmarshal` handles this transparently:
a JSON `true`/`false` unmarshals correctly into a `*bool` field, and an
absent field leaves the `*bool` as `nil` (which is the desired "never
set" signal). No custom JSON unmarshaling is needed for `bool` to `*bool`
changes.

For truly incompatible type changes (e.g., changing from `string` to
`struct`), the `CosmosToInternal` layer may need to handle both the old
and new JSON shapes during a transitional period. The specifics of such
migrations are out of scope for this DDR.

### Default value consistency

Default values are currently hardcoded as literals in multiple locations
(`NewDefault*()` constructors, `SetDefaultValues*()` per API version,
storage defaults in `CosmosToInternal*()`, and CS→RP conversion defaults
in `internal/ocm/convert.go`). There is no shared constant or
compile-time mechanism to prevent drift. Consider adding a cross-layer
consistency test that verifies all defaulting layers produce identical
values for each field:

1. Create an object via `NewDefault*()` constructor.
2. Create an object via `SetDefaultValues*()` + `ConvertToInternal()`.
3. Create an object via `CosmosToInternal*()` from a zero-valued Cosmos
   document.
4. Create an object via the CS→RP conversion from a CS response with
   missing/empty values.
5. Assert all four have identical defaults for each defaulted field.

This test should account for intentional per-version differences (e.g.,
older API versions that don't expose a field use `normalize*()` to force
the default during conversion, not `SetDefaultValues*()`). The CS→RP
consistency check is especially important because FE GET responses
source most operational fields from CS while BE sources them from
Cosmos — if these disagree, FE and BE silently diverge.

### Default value changes

If a field's default value needs to change in a future API version, the
storage defaulting function must not be updated to use the new default —
doing so would change the meaning of pre-existing documents. The storage
default is always frozen to the value from the API version that **first**
introduced the field. New API version defaults should be applied only in
that version's `SetDefaultValues*()` function on the write path. The
storage defaulting function only fires when the stored value is the zero
value, so new documents written with a different default via
`SetDefaultValues*()` will not be affected by the storage default.

## References

### Kubernetes

- [`Scheme.Default()`][k8s-scheme-default] — applies registered defaulting
  functions by type
- [Versioning codec `Decode`][k8s-versioning] — calls `Default()` on the
  decoded object in both decode branches (lines 170, 187)
- [`SetDefaults_Deployment`][k8s-apps-defaults] — example per-version
  defaulting function
- [StorageVersionMigrator][k8s-svm] — optional background migration
  controller
- [kubernetes/enhancements#4192](https://github.com/kubernetes/enhancements/issues/4192) —
  in-tree storage version migrator enhancement tracking

### ARO Classic

- [`api.SetDefaults()`][aro-setdefaults] — document-level defaults
- [Write-path call site][aro-putorpatch-defaults] — `SetDefaults` before
  Cosmos write
- [Read-path enricher call][aro-get-enrich] — `clusterEnricher.Enrich()`
  on GET
- [Per-enricher `SetDefaults`][aro-enricher-setdefaults] — called inside
  each enricher
- [`ensureDefaults()`][aro-ensuredefaults] — background maintenance via
  `PatchWithLease`

### ARO-HCP

- [`MigrateCosmosOrDie`](https://github.com/Azure/ARO-HCP/blob/a98a017a0b7ab57bf2a30a25f07d1b8f729c45e4/frontend/pkg/frontend/migrate_cosmos.go) —
  startup migration, Get→Replace round-trip
- [`MigrateCosmosOrDie` call site](https://github.com/Azure/ARO-HCP/blob/a98a017a0b7ab57bf2a30a25f07d1b8f729c45e4/frontend/pkg/frontend/frontend.go#L155) —
  runs before HTTP server starts
- [`CosmosToInternalNodePool`](https://github.com/Azure/ARO-HCP/blob/a98a017a0b7ab57bf2a30a25f07d1b8f729c45e4/internal/database/convert_nodepool.go#L75-L110) —
  raw copy, no defaults (the gap this DDR addresses)
- [`NewDefaultHCPOpenShiftClusterNodePool`](https://github.com/Azure/ARO-HCP/blob/a98a017a0b7ab57bf2a30a25f07d1b8f729c45e4/internal/api/types_nodepool.go#L109-L126) —
  default values for new documents
