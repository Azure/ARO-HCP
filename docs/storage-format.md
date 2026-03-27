# DDR: Cosmos DB Storage Format

- **Status:** Decided
- **Date:** 2026-03-25
- **Authors:** @sudobrendan
- **Complements:** [API Version Defaults and Storage](api-version-defaults-and-storage.md)
- **Context:** PR #4219 left "Storage format TBD." This DDR closes that gap.

## Previous Decisions

### Overlay + Reset-Fields Will Replace Conversion

> **Implementation status:** This section describes the target design. The overlay+reset-fields mechanism is not yet implemented — `ConvertToInternal` remains active. Implementation is tracked as acceptance criterion 3 in [AGENT_TODO.md](AGENT_TODO.md).

Per-API-version conversion functions (`ConvertToInternal`) will be replaced by
a generic three-step mechanism:

1. **Load**: The existing internal type is read from Cosmos via the per-resource-type conversion function (`CosmosToInternalCluster`, `CosmosToInternalNodePool`, `CosmosToInternalExternalAuth`) including `EnsureDefaults`. This is the base for preservation.
2. **Reset-fields**: Each API version will declare the set of field paths it "owns" via a `GetResetFields`-style function. Only the owned-set fields on the internal type are zeroed — fields outside the owned set remain at their stored Cosmos values.
3. **Overlay**: The incoming request body is unmarshaled into the versioned external struct. The external struct's owned-field values are then copied onto the zeroed owned-set positions in the internal type. Fields outside the owned set were never zeroed and are not touched by this step.

No *per-version* type translation code is written — the per-resource-type deserialization functions (`CosmosToInternalCluster`, `CosmosToInternalNodePool`, `CosmosToInternalExternalAuth`) remain as the single shared deserialization path per resource type.

This means:
- New API versions only declare what they own — once, when the version is created. Old versions will never need updating when new versions add fields.
- `preserveUnknownFields` helpers (e.g. `preserveUnknownClusterFields`) will be eliminated — field preservation will be a structural property of the mechanism, not a hand-maintained list.
- No new code should implement `ConvertToInternal` for API version conversion.

### Additive-Only Contract

The following invariants govern all three internal types —
`api.HCPOpenShiftCluster`, `api.HCPOpenShiftClusterNodePool`, and
`api.HCPOpenShiftClusterExternalAuth` — and their embedded structs. Violating
any of them risks silent data corruption on Cosmos read-back.

1. **Fields are never removed** from any internal type or its embedded
   structs. Deprecated fields stay in the struct with a comment.

2. **JSON tags are never changed.** Renaming a tag orphans existing Cosmos
   data under the old key.

3. **Field types are never changed.** A `string` to `int32` change, for
   example, causes `json.Unmarshal` to silently zero the field on read-back.

4. **The embedding chain is never restructured.** `HCPOpenShiftCluster`
   embeds `arm.TrackedResource` (which embeds `arm.Resource`);
   `HCPOpenShiftClusterNodePool` also embeds `arm.TrackedResource`;
   `HCPOpenShiftClusterExternalAuth` embeds `arm.ProxyResource` (which
   embeds `arm.Resource`). These are bare (non-pointer) embeddings, so their
   fields promote to the top-level JSON object. Wrapping an embedding in a
   named field, changing it to a pointer, or inlining promoted fields
   silently changes the wire format of every promoted field (`id`, `name`,
   `type`, `systemData`, and for tracked resources: `location`, `tags`).

5. **New bool/int fields use pointer types with `omitempty`.** `*bool` and
   `*int32` with `omitempty` distinguish nil (field omitted from Cosmos) from
   false/0 (explicit customer value). Without the pointer, value types with
   `omitempty` drop `false` and `0` during serialization. Without `omitempty`,
   a nil pointer serializes as `"field": null` instead of being omitted.

6. **`EnsureDefaults()` is append-only and zero-value-only.** It must only
   set a field when the current value is the zero value. It must never
   overwrite an explicitly-set non-zero value. It must never be reduced
   (rules removed).

7. **Fields tagged `json:"-"` are excluded** from these invariants. They are
   not serialized to Cosmos.

8. **`_etag` is Cosmos-managed and excluded.** It is not part of the Go
   struct serialization.

**Legacy debt (cluster):** `HostPrefix int32`, `NodeDrainTimeoutMinutes int32`,
and `ClusterAutoscalingProfile` fields (`MaxNodesTotal`,
`MaxPodGracePeriodSeconds`, `MaxNodeProvisionTimeSeconds`,
`PodPriorityThreshold`) use value types with `omitempty`.

**Legacy debt (nodepool):** `AutoRepair bool`, `Replicas int32`, and
`NodePoolAutoScaling.Min/Max int32` use value types with `omitempty`.

**Legacy debt (externalauth):** None. All fields are strings, slices, or
structs — no bare bool/int with `omitempty`.

Zero values (`false`, `0`) are silently dropped during Cosmos serialization
in the cluster and nodepool cases above. New fields MUST NOT repeat this
pattern in any resource type.

**Correct example:** `MirrorSourcePolicy` on `ImageDigestMirror` is a
`string`-aliased enum stored in Cosmos but not yet exposed in any
customer-facing API version. It demonstrates the additive-only pattern:
the field exists in the internal type with a canonical default in
`EnsureDefaults()`, so existing documents get a correct value on read-back
without migration. This is a reference for the pattern, not the contract
itself.


## Storage Constraints

Any storage format must account for the following constraints. These are not
negotiable — they are properties of the system that any solution must satisfy.

### Multi-Reader Problem

Three independent readers consume Cosmos documents, each with different
constraints:

- **Frontend** is API-version aware. It deserializes documents, projects them
  into the requested API version for reads, and must preserve fields from other
  API versions on writes (ARM backward compatibility).
- **Backend** is API-version unaware by design. It reads the Cosmos document,
  modifies `ProvisioningState` and `ActiveOperationID`, and writes back. It
  does not consult Cluster Service during the read path. Backend is also a
  secondary migration vector: `EnsureDefaults` is applied during the read
  (inside the `CosmosToInternal*` functions), and when Backend subsequently writes the
  document back, those defaults are persisted as a side effect.
- **Admin API** (`cosmosdump`) reads the raw Cosmos document and logs it. It is
  read-only with respect to cluster content. Breakglass operations route
  through Cluster Service APIs directly.

Any storage format that requires per-document version dispatch forces Backend
and Admin API to become API-version aware — a fundamental design change.

### Forward Read

A newer API version GET on a resource written by an older version must handle
fields that did not exist when the document was written. Under the
additive-only contract, new fields are always optional with canonical defaults
via `EnsureDefaults()` — so this is a non-issue as long as new API versions do
not introduce required customer-provided fields with no canonical default.
Breaking this convention would be an API design violation regardless of storage
format.

### Backward Write

ARM's backward compatibility requirement states that a PUT via an older API
version MUST NOT destroy fields that version does not know about. This is the
"scariest case" for any multi-version RP.

Concrete scenario: a cluster is created via v20251223preview with
`imageDigestMirrors` (a field absent from v20240610preview). A customer then
issues a PUT via v20240610preview. The storage format and conversion mechanism
must guarantee that `imageDigestMirrors` survives this write.

A similar (less complex) issue exists for backward PATCH.

### Operational Fields

Each resource type has service-provider-only fields with no external analog:
`ClusterServiceID`, `ActiveOperationID`, `ExperimentalFeatures`, and
`ManagedIdentitiesDataPlaneIdentityURL` on clusters;
`ClusterServiceID` and `ActiveOperationID` on nodepools and external auths.
These live in per-type `ServiceProviderProperties` structs in the internal
types. Exposing them to customers via the ARM API surface would be a security
and privacy violation. Any storage format must provide a home for these fields.

### API Version Growth

ARO-HCP expects frequent API releases. Unlike Kubernetes, which has a small
number of long-lived API versions, the RP will accumulate many versions over
time. Any approach where read-path complexity grows O(N) with the number of
API versions may create long-term impact.

## Decision

**Storage format: the internal types — `api.HCPOpenShiftCluster`,
`api.HCPOpenShiftClusterNodePool`, and `api.HCPOpenShiftClusterExternalAuth` —
remain the Cosmos storage schema for their respective resource types.**

The internal type satisfies all constraints above:
- **Multi-reader**: All three readers deserialize the same type. No version
  dispatch needed. Read path is O(1) regardless of API version count.
- **Operational fields**: They live naturally in the internal type alongside
  customer-facing fields.
- **API version growth**: Adding a new API version adds fields to the internal
  type (additive-only) and declares a new owned-set. No existing reader code
  changes.

**Forward read** (newer API version reads older document):
1. `CosmosToInternal*` deserializes the document — all stored fields load.
2. Fields added after the document was written have zero values.
3. `EnsureDefaults()` fills canonical defaults for zero-valued fields.
4. Result: complete internal type, no version dispatch, no data loss.

**Backward write** (older API version writes to newer document):
1. `CosmosToInternal*` loads the existing document — all fields including newer ones are present.
2. Reset-fields zeroes only the writing version's owned-set fields.
3. Overlay copies request body values onto the zeroed positions.
4. Fields outside the owned set retain their stored values.
5. Result: no data loss, no version-specific conversion code.

### What the Internal Type Is

The Cosmos document structure nests the internal API type:

```
HCPCluster (TypedDocument)
  └─ HCPClusterProperties
       ├─ ResourceDocument (inline: ARM metadata, tags, identity, provisioning state)
       ├─ CosmosMetadata (redundant resourceId for query purposes)
       ├─ IntermediateResourceDoc (migration artifact — stop inlining ResourceDocument)
       └─ ClusterInternalState
            └─ InternalAPI: api.HCPOpenShiftCluster

NodePool (TypedDocument)
  └─ NodePoolProperties
       ├─ ResourceDocument (inline)
       ├─ CosmosMetadata
       ├─ IntermediateResourceDoc
       └─ NodePoolInternalState
            └─ InternalAPI: api.HCPOpenShiftClusterNodePool

ExternalAuth (TypedDocument)
  └─ ExternalAuthProperties
       ├─ ResourceDocument (inline)
       ├─ CosmosMetadata
       ├─ IntermediateResourceDoc
       └─ ExternalAuthInternalState
            └─ InternalAPI: api.HCPOpenShiftClusterExternalAuth
```

All three resource types share the same nesting pattern. The additive-only
invariants apply equally to all three internal types.

`_etag` is a Cosmos-managed field on the raw JSON document. It is read into
`BaseDocument.CosmosETag` on the Go side and carried through to each internal
type's `CosmosETag` field (tagged `json:"-"`) for conditional Replace. It is
not part of the Go struct serialization and is not subject to the additive-only
invariants.

Cosmos is configured with **Session consistency**
(`defaultConsistencyLevel: 'Session'` in `dev-infrastructure/modules/rp-cosmos.bicep`).
ETag-based optimistic concurrency (If-Match / 412) is independent of the consistency
level — it is an atomic server-side partition-level check under all consistency settings.
For data flow details, see the [Data Flows](api-version-defaults-and-storage.md#data-flows)
section of the companion DDR.

## Alternatives Considered

### Option A: Store vLatest External Type

Store the latest API version's external type directly in Cosmos, eliminating
the internal type.

**Forward read** (newer API version reads older document):
1. Stored type IS vLatest — forward read within the current vLatest is trivial.
2. When vLatest advances, existing documents lack new fields. `json.Unmarshal` zeros them.
3. All documents require migration to populate new fields.
4. Result: works only if eager migration completes before any read of the new version.

**Backward write** (older API version writes to newer document):
1. An older API version PUT arrives. The request body lacks fields from newer versions.
2. Without an internal type: the write stores the older struct. Newer fields are silently destroyed. **Data loss.**
3. With an internal type intermediate: works, but reintroduces the internal type — defeating the purpose of Option A.

**Rejected because:**
- Operational fields (`ClusterServiceID`, `ActiveOperationID`,
  `ExperimentalFeatures`, `ManagedIdentitiesDataPlaneIdentityURL`) have no
  external analog by design — they are internal operational state that should
  never be customer-visible. They would need a side-car struct, defeating
  the purpose.
- vLatest is unstable — every time the latest version changes, all documents
  need migration. There is no per-document schema version tag.
- **Pros:** eliminates internal type; ARM-friendly field names in cosmosdump.

### Option B: Store External Versioned Types (the Kubernetes Way)

In Kubernetes, CRDs designate one external API version as the storage version.
Incoming objects are converted to that version before etcd write; outgoing
reads are converted from the storage version to the requested serving version
by the versioning codec on every read. Objects stored under an older storage
version remain in etcd at their written version — the StorageVersionMigrator
must explicitly rewrite them when the storage version advances. The Kubernetes
variant stores a versioned external struct in the backing store — the hub type is used only as an in-memory conversion intermediate and is never persisted.

Applied to ARO-HCP, this alternative has two distinct sub-variants:

**Sub-variant B1 — Per-document versioned storage.** Each Cosmos document
carries a `storageVersion` field identifying which API version's schema it was
written with. A v20240610preview PUT stores a `v20240610preview` external
struct; a v20251223preview PUT stores a `v20251223preview` external struct.
Documents for the same resource can coexist in different versions as the API
evolves.

**Sub-variant B2 — Single designated storage version.** All documents are
converted to one pinned external API version (e.g., `v20251223preview`) before
write, mirroring the Kubernetes CRD model exactly. Advancing the storage
version requires explicit migration — the StorageVersionMigrator must rewrite all existing documents; objects remain stored at their written version until that explicit rewrite completes.

Both sub-variants share the core property: the stored format IS an external API
type, not `api.HCPOpenShiftCluster`. The external types are Autorest/Kiota-
generated structs in `internal/api/v20240610preview/generated/` and
`internal/api/v20251223preview/generated/`.

#### Forward Read

**B1 (per-document versioned):**
1. Reader checks `storageVersion` on the document to select the correct deserializer.
2. Older-version documents lack newer fields — defaults must be applied post-deserialization.
3. Every reader must implement version dispatch — O(N) fan-out with API version count.
4. Result: works, but adds permanent read-path complexity that grows with each API version.

**B2 (single designated storage version):**
1. Reader deserializes using the pinned storage version's struct.
2. Conversion to serving version fills defaults for missing fields.
3. Unmigrated documents (written before the current storage version) remain at their old version until explicitly rewritten by a StorageVersionMigrator equivalent.
4. Result: works if migration is complete; stale documents may produce incorrect results until migrated.

#### Backward Write — the ARM data-loss failure mode

ARM's backward compatibility requirement states that a PUT via an older API
version MUST NOT destroy fields that version does not know about. This is the
"scariest case" documented in the sequence diagrams above.

Concrete scenario: a cluster is created via v20251223preview. The Cosmos
document includes `imageDigestMirrors` (a field absent from v20240610preview).
A customer then issues a PUT via v20240610preview.

**Without an internal type (naive B1/B2 — broken):**

1. Reader unmarshals the stored document into a `v20240610preview` Go struct.
2. `imageDigestMirrors` has no corresponding field in the v20240610preview
   struct — `json.Unmarshal` silently discards it.
3. The subsequent write stores a v20240610preview struct with
   `imageDigestMirrors` absent.
4. **The field is permanently destroyed** — not merely unreadable by the older
   client, but gone from Cosmos.

B2 has the same failure at write time: the incoming v20240610preview request
body is converted to the designated storage version, but the conversion has no
source data for `imageDigestMirrors`, so it writes nil over the existing value.

**With an internal type intermediate (works, but extra steps):**

1. Reader unmarshals the stored document into the internal type — all fields
   including `imageDigestMirrors` are preserved.
2. Overlay + reset-fields applies the v20240610preview PUT — only the fields
   v2024 owns are touched; `imageDigestMirrors` is untouched.
3. The internal type is converted back to the external storage version for
   write.
4. **No data loss** — the mechanism works correctly.

But this reintroduces the internal type as a required intermediate on every
read and write path. Backend and Admin API already need the internal type (see
below). At that point, the external storage format adds two conversion steps
(external→internal on read, internal→external on write) with no benefit over
storing the internal type directly — which is what the Decision section adopts.

#### Operational fields have no home

Service-provider-only fields (`ClusterServiceID`, `ActiveOperationID`,
`ExperimentalFeatures`, etc.) live in per-type `ServiceProviderProperties`
structs in the internal types. These fields have no external analog by design:
exposing them to customers via the ARM API surface would be a security and
privacy violation. Under Option B, they
must live in a side-car struct stored alongside the external document, or be
inlined under a vendor-namespaced key. Either approach recreates a two-part
document structure (external fields + operational fields) that is structurally
identical to the current `ClusterInternalState` nesting. The net result is the
same document shape with more code to assemble it.

#### Multi-reader impact

Backend is API-version unaware by design: it reads the Cosmos document,
modifies `ProvisioningState` and `ActiveOperationID`, and writes back. Under
Option B, Backend must either (a) know which external version to deserialize
into for each document it reads, requiring per-document schema dispatch, or (b)
use `map[string]any` and patch fields by key — losing all type safety. The
Admin API `cosmosdump` similarly reads raw documents; versioned storage makes
the dump format non-uniform across documents in the same collection.

#### Comparison to Option A

Option A (store vLatest external type) is the degenerate case of B2 where the
designated storage version is always advanced to match vLatest. B2 with a
stable, explicitly pinned storage version avoids Option A's instability problem
(documents need migration every time a new API version becomes latest), but it
does not escape B2's backward-write data-loss failure. The backward-write
failure mode described above applies to B2 regardless of whether the storage
pin is stable or moving. B1's claimed advantage over Option A is that
per-document versioning avoids eager migration when the storage version
advances. In practice this trades the migration cost for permanent read-path
version dispatch complexity: every reader must branch on `storageVersion` to
select a deserializer. For a long-lived RP with N API versions this fan-out
grows with N, whereas the internal type approach keeps the read path O(1)
regardless of how many API versions exist.

**Rejected because:**
- **Backward write requires the internal type anyway.** Without an internal
  type intermediate, a PUT via an older API version silently destroys fields
  the writing version does not know about. With an internal type intermediate,
  backward writes work correctly — but the external storage format then adds
  two unnecessary conversion steps (external→internal on read,
  internal→external on write) with no benefit over storing the internal type
  directly.
- **Operational fields have no natural home** in an external type; a mandatory
  side-car struct recreates the current two-part document shape with additional
  indirection.
- **Backend and Admin API** require per-document version dispatch or lose type
  safety entirely.
- **Read-path fan-out grows O(N) with API versions.** Under B1, every reader
  must branch on `storageVersion` to select a deserializer. ARO-HCP expects
  frequent API releases — unlike Kubernetes, which has a small number of
  long-lived API versions. The internal type approach keeps the read path O(1)
  regardless of how many API versions exist.
- **Pros:** document field names match ARM API names exactly, simplifying
  `cosmosdump` output for SREs; Kubernetes CRD precedent for the pattern.
- These pros do not outweigh the complexity cost. The internal type is
  required regardless (Backend, operational fields, backward-write safety),
  so the external storage format adds conversion overhead without eliminating
  the internal type. On the migration tooling problem: safely advancing the
  storage version under live traffic requires the Kubernetes
  StorageVersionMigrator — a first-class primitive that sweeps etcd and
  rewrites all objects with completion guarantees. Cosmos has no equivalent;
  `MigrateCosmosOrDie` is additive-only (backfilling defaults) and does not
  have the batching, progress tracking, or idempotency needed for structural
  schema migration under live traffic.

## Why Kubernetes Stores External Types (and Why We Don't)

Kubernetes stores versioned external types in etcd — not the internal hub
type — and converts through the hub on every read and write. This is a
deliberate design choice. Understanding *why* Kubernetes pays this cost
clarifies why ARO-HCP does not need to.

### Why Kubernetes needs hub-type instability

The Kubernetes hub type has no serialization contract. It can be freely
restructured between releases because it is never persisted to etcd. This
instability serves five purposes:

**1. Structural API redesign (alpha → beta → GA).** Kubernetes API versions
undergo genuine structural changes between maturity phases — not just field
additions. Examples:

- **Ingress** (`extensions/v1beta1` → `networking.k8s.io/v1`): `spec.backend`
  was renamed to `spec.defaultBackend`. `servicePort` changed from
  `IntOrString` to a `ServiceBackendPort` struct with separate `number` and
  `name` fields. `pathType` was added as a required enum. These are new Go
  types, not new fields on existing types.
- **Label selectors** (ReplicationController → ReplicaSet): the selector
  changed from `map[string]string` (equality-only) to `LabelSelector` (with
  `matchLabels` and `matchExpressions`). The old type could not express
  set-based matching — a new struct was required.

The hub absorbs these structural disagreements so that conversion functions
can map cleanly between external versions without each version knowing about
every other version.

**2. Version removal shrinks the hub.** When `extensions/v1beta1` Deployment
was removed in Kubernetes 1.16, its `RollbackTo *RollbackConfig` field —
absent from `apps/v1` — became dead code. Because the hub is not persisted,
fields whose only consumers have been removed can be cleaned up. If the hub
were stored in etcd, those fields would persist in every document
indefinitely.

**3. No serialization identity.** The hub type's version string
(`__internal`) is explicitly documented as "should not be considered stable
or serialized." The GVK is cleared on conversion to internal. JSON and
protobuf serializers have no registered encoding for internal types. The hub
was never designed for persistence — it is a compile-time and runtime
convenience.

**4. Rollback safety.** The hub type changes between Kubernetes releases. If
it were stored in etcd, rolling back the API server to a previous release
could produce undecodable objects — the older binary's hub struct would not
match the stored bytes. External versioned types have explicit compatibility
guarantees, so rollback is safe.

**5. Self-describing storage.** etcd data encoded as a versioned external
type can be decoded by tooling (e.g.,
[auger](https://github.com/etcd-io/auger)) using published API schemas,
without the exact API server binary. This enables disaster recovery and
forensic inspection during outages.

### Why these pressures do not apply to ARO-HCP

ARO-HCP operates under ARM's backward compatibility contract, which creates
a fundamentally different constraint landscape.

**1. No structural redesign (for GA).** ARM GA API versions are
contractually additive — new versions add fields; they do not rename,
restructure, or remove them. Preview versions have no such guarantee: ARM
allows breaking changes to previews at any time, and the KMS
`VaultName` promotion from `KmsKey` to `KmsEncryptionProfile` between
v20240610preview and v20251223preview already demonstrates this. The hub
type absorbs these preview-era structural divergences without restructuring
itself — `VaultName` stays on `KmsKey` (its original location), and each
version's conversion functions handle the mapping. This is the same
mechanism Kubernetes uses, but the scale of structural change is
dramatically smaller and the hub never needs to follow an external version's
restructuring. Once a GA version ships, the fields it introduced are
permanent in the hub.

**2. Versions are rarely removed.** ARM preview versions can be retired
after ~90 days; GA versions after 3+ years. In practice, ARM RPs support
many versions simultaneously (e.g., Microsoft.Compute/virtualMachines has
27 versions spanning a decade). The "hub shrinks when old versions die"
pressure that drives Kubernetes hub evolution is much weaker for an ARM RP.

**3. The internal type already has a serialization contract.** The
additive-only invariants (see above) give the hub a stable serialization
format. This is the opposite of Kubernetes's unstable hub — and it is
deliberate. The tradeoff: we give up the freedom to restructure the hub
in exchange for O(1) reads, no version dispatch, and no double-conversion
overhead.

**4. Rollback is structurally simpler.** ARO-HCP Frontend pods are deployed
via rolling update (`maxSurge: 50%`, `maxUnavailable: 50%`). Old and new
pods coexist briefly during deployment, but all pods converge to the same
binary. The additive-only contract means a rolled-back binary can still
deserialize documents written by the newer binary — new fields are silently
ignored by `json.Unmarshal`, and `EnsureDefaults()` re-fills them when the
newer binary is redeployed. Kubernetes cannot rely on this because its hub
type undergoes structural changes (field renames, type changes) that are
incompatible across versions.

**5. The internal type is required regardless.** Even under Option B, the
internal type would still be needed as a conversion intermediate for
backward-write safety, operational fields, and multi-reader support.
Storing external types would add two conversion steps (external→internal on
read, internal→external on write) without eliminating the internal type. We
accept the additive-only constraint because it is the price of a simpler
architecture — and it is a price already paid.

### Concrete examples from this codebase

The internal type already exercises the patterns Kubernetes's hub supports,
but within the additive-only constraint:

- **Field restructuring across versions:** `KmsKey.VaultName` lives on
  `KmsKey` in the hub (`types_cluster.go:186`) and in v20240610preview, but
  was promoted to `KmsEncryptionProfile.VaultName` in v20251223preview. The
  hub does not follow the restructuring — it absorbs the disagreement, and
  each version's conversion functions handle the mapping independently.

- **Richer internal types:** `SubnetID` is `*azcorearm.ResourceID` in the
  hub (`types_cluster.go:136`) but `*string` in external types. The hub uses
  parsed, validated types for programmatic safety — the same pattern
  Kubernetes uses with `resource.Quantity` (string on the wire, structured
  type internally).

- **Forward storage:** `MirrorSourcePolicy` on `ImageDigestMirror`
  (`types_cluster.go:223`) exists in the hub with a canonical default in
  `EnsureDefaults()`, but is not exposed in any current API version. When a
  future version surfaces this field, existing documents will already have
  the correct value — no migration needed.

### The accepted tradeoff

Kubernetes's hub instability is a design *for schema evolution* — the
freedom to restructure when understanding of a resource matures across
alpha, beta, and GA phases. ARO-HCP's hub stability is a design *for
operational simplicity* — accepting that the internal type grows
monotonically in exchange for zero conversion overhead and a single
deserialization path for all readers.

The long-term risk of monotonic growth is real but manageable:
- The hub is ~96 fields today across cluster and nodepool types.
- Growth rate is ~3-5 fields per API version.
- The Cosmos 2MB document limit is far from binding.
- `EnsureDefaults()` provides a single canonical location for default
  management, append-only by policy.

#### Risks and limits of the additive-only commitment

**The additive-only contract is self-imposed, not ARM-mandated.** ARM
requires wire format stability of GA API versions. The additive-only
invariants on the internal type are how ARO-HCP achieves that, but they
are more conservative than strictly necessary. For fields that are
internal-only (tagged `json:"-"`) or for preview-only fields that have
not yet shipped to customers, violating the invariants is a deliberate
trade-off, not a disaster.

**Preview vs GA stability.** ARM preview API versions have zero stability
guarantees — breaking changes are permitted at any time. The KMS
`VaultName` restructuring between v20240610preview and v20251223preview
already demonstrates this. The additive-only contract binds most tightly
once a GA version ships and its field layout becomes the hub's permanent
shape. Before GA, the team has room to correct layout mistakes in the
internal type if the correction is coordinated with a `MigrateCosmosOrDie`
sweep and no customer-facing GA version depends on the old layout.

**One active preview at a time.** ARO-HCP's policy is to have at most one
non-deprecated preview API version active at any given time. This
significantly constrains the version overlap window and reduces the risk
surface of the additive-only commitment in several ways:

- **Bounded preview overlap.** When a new preview ships, the previous
  preview is deprecated. There is never a period where three or more
  preview versions must coexist. This means the hub only needs to be
  forwards-compatible with one preview at a time, not an unbounded set.
- **Hub can evolve between preview cycles.** Because the old preview is
  deprecated before the new one ships, the team can restructure the hub
  between cycles — fix field layout mistakes, clean up experimental fields,
  and coordinate a `MigrateCosmosOrDie` sweep — without breaking any
  supported API version. Preview-era mistakes are correctable.
- **Structural ghosts are bounded to GA.** Fields that become permanent
  dead weight in the hub only accumulate from GA versions. Preview fields
  that prove to be misdesigned can be corrected or removed in the next
  preview cycle. The additive-only contract permanently commits only at GA.
- **Overlay + reset-fields validates incrementally.** Each new preview
  introduces one new `FieldSet` definition. With only one active preview
  and one (future) GA version, the team validates each `FieldSet` in
  isolation rather than reasoning about combinatorial interactions across
  many concurrent versions.

**Security and compliance exceptions.** The invariant "fields are never
removed" is not absolute. If compliance or security requires purging a
field from storage (not just zeroing it, but preventing it from being
deserialized from backup-restored data), changing the JSON tag to
`json:"-"` may be necessary — even though this violates invariant 2. The
additive-only contract is a strong default, not a legal obligation. The
bar for violating it should be high: security/compliance mandate, with a
coordinated migration and explicit documentation of the exception.

**Incident response degradation.** As the schema grows over years, raw
Cosmos document dumps become harder to interpret during incidents. An SRE
reading a 200-field document cannot easily distinguish current fields from
deprecated ones. This is a real reliability cost, not just a developer
experience concern.

**Structural migration infrastructure does not yet exist.** The current
`MigrateCosmosOrDie` is viable for additive backfills (applying
`EnsureDefaults` to existing documents) at the current fleet size. It is
*not* viable for structural schema migration at production scale — it does
a full table scan with panic-on-failure, no progress tracking, and no
idempotency. The code itself acknowledges this: *"Once datasets are large,
we will start doing this inside of the backend."* If the additive-only
contract is ever broken, a purpose-built migration mechanism with batched
processing, progress tracking, idempotency markers, and rollback support
must be built first — comparable to the Kubernetes StorageVersionMigrator.

**Additive-only is a strong commitment, not an absolute guarantee.** The
practical prediction is that additive-only will hold for the lifetime of
the service under normal operation. If it ever becomes necessary to break
it (field type correction, structural debt cleanup, compliance-mandated
purge), the migration infrastructure must be built before execution, and
the exception must be documented as a one-time, coordinated operation.

## Known Gaps

1. **`omitempty` on bool/int fields.** Pre-existing debt in cluster and
   nodepool types (see Legacy debt notes in Additive-Only Contract above).
   ExternalAuth has no such debt.
   All affected fields drop zero values during serialization. New fields in
   any resource type must not repeat this pattern.
   Disposition: document and prevent recurrence.

2. **Backend concurrent write race.** Frontend and Backend may read the same
   document concurrently. Both use ETag-conditional Replace, so the loser
   gets a 412. Pre-existing, bounded risk because Backend only modifies
   ProvisioningState and ActiveOperationID. Disposition: acceptable; the
   controller reconciliation loop provides implicit re-entry when
   `UpdateOperationStatus` returns an error on a 412. Note: the
   `patchOperation` function has a stale TODO comment at `utils.go:303`
   suggesting unconditional Replace, but the underlying `replace()`
   implementation at `crud_helpers.go:380-381` wires the ETag conditionally;
   the TODO should be removed to avoid confusion.

3. **`MigrateCosmosOrDie` concurrent migration.** During rolling deployment,
   two Frontend pods may attempt to Replace the same document. The loser gets
   a Cosmos conflict error (412 on ReplaceItem or 409 during ID migration),
   which causes a `panic()` and pod restart. The restarted pod re-runs
   migration successfully. Pre-existing, self-correcting. Disposition:
   acceptable; a non-fatal 412 handler would improve robustness by avoiding
   unnecessary pod restarts during rolling deployments.

4. **DELETE crash-consistency gap.** If Cosmos batch fails after CS deletion
   succeeds, the cluster is gone from CS but still present in Cosmos. Backend
   reconciliation partially recovers: it deletes the resource document from
   Cosmos, then patches the operation to Succeeded (see diagram above).
   Pre-existing. Disposition: tracked separately; full fix requires reversing
   the CS-then-Cosmos order or adding a compensating mechanism.

5. **Cosmos 2MB document size limit.** All three internal types serialize
   well under 2MB today. Not a near-term concern. Disposition: monitor.

6. **Structural migration infrastructure.** `MigrateCosmosOrDie` is viable
   for additive backfills at current fleet size. It is not viable for
   structural schema migration at production scale (no batching, no progress
   tracking, no idempotency, panics on conflict). If the additive-only
   contract is ever broken, a purpose-built migration mechanism must be
   built first. Disposition: acceptable for now; revisit when fleet size
   or structural debt warrants it.

7. **Security/compliance field purge.** The additive-only contract has no
   mechanism for making a field undeserializable (as opposed to merely
   zeroing it). If a compliance mandate requires that a field cannot be
   reconstituted from backup-restored data, changing its JSON tag to
   `json:"-"` would violate invariant 2. Disposition: no current need;
   document the exception path if it arises (coordinated migration +
   explicit invariant violation with justification).

## References

- [API Version Defaults and Storage DDR](api-version-defaults-and-storage.md)
- [PR #4219](https://github.com/Azure/ARO-HCP/pull/4219)
- [ARM RPC backward compatibility requirements](https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/resource-api-reference.md)
- [Kubernetes storage versioning (sig-api-machinery internal hub type)](https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/storage/storagebackend/factory/)
- [Kubernetes API changes guide — rollback and storage version policy](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api_changes.md)
- [Kubernetes issue #20193 — steps to remove `__internal` APIs](https://github.com/kubernetes/kubernetes/issues/20193)
- [auger — decode Kubernetes objects from etcd](https://github.com/etcd-io/auger)
- [`dev-infrastructure/modules/rp-cosmos.bicep`](../dev-infrastructure/modules/rp-cosmos.bicep)
