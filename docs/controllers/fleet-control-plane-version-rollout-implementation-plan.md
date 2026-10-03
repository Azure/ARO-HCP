# Fleet Control Plane Version Rollout — Implementation Plan

This plan maps the fleet rollout design originally authored on the
`z-stream-rollout` branch onto the current ARO-HCP codebase. It
identifies what already exists, what is net-new, and the concrete controllers,
types, config, wiring, and tests required.

> Status: the seven rollout controllers plus catalog publication and retirement,
> Cosmos storage, informers, and backend wiring
> are implemented. They run unconditionally. Production policy is hardcoded;
> risk filtering, environment configuration, and the Admin API pin setter remain follow-ups.
> Structured rollout profiles, legacy backfill, and discovery/reference seeding are
> implemented. Frontend version GET/LIST still use Cluster Service; catalog read
> cutover is not implemented yet.

## 1. Background: the pipeline before this change

Before this change, the backend used this per-cluster pipeline. The
`ControlPlaneDesiredVersion` controller has now been removed; its graph selection
helpers remain in `select_control_plane_version.go`.

| Concern | Existing code | Reads | Writes |
|---|---|---|---|
| Resolve a concrete z-stream for a cluster from its desired minor + channel, using the Cincinnati upgrade graph | `backend/pkg/controllers/cluster/version/control_plane_desired_version_controller.go` (`ControlPlaneDesiredVersion`) | `HCPOpenShiftCluster.CustomerProperties.Version.{ID,ChannelGroup}`, `ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions`, Cincinnati | `ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion` |
| Push the desired version to cluster-service (post a `ControlPlaneUpgradePolicy`) | `backend/pkg/controllers/cluster/version/trigger_control_plane_upgrade_controller.go` (`TriggerControlPlaneUpgrade`) | `Spec…DesiredVersion` vs `Status…ActiveVersions[0]` | CS `ControlPlaneUpgradePolicy` |
| Mirror observed HostedCluster versions back onto the ServiceProviderCluster | `backend/pkg/controllers/cluster/version/control_plane_active_version_controller.go` (`ControlPlaneActiveVersions`) | HostedCluster `status.controlPlaneVersion.history` / `status.version.desired.channels` | `ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions`, `Status.DesiredVersionChannels` |
| Query the upgrade graph | `backend/pkg/controllers/controlplaneversion/` — recency-based graph selection, reused through `cluster/version/SelectControlPlaneVersion` | — | — |
| Convert a semver into a CS version id (`4.21` → `openshift-v4.21.20`) | `internal/ocm/client.go` `NewOpenShiftVersionXYZ`, `internal/ocm/convert.go` `clusterCSVersionID` | `Spec…DesiredVersion` | CS cluster/nodepool version id |

Relevant existing API types (`internal/api/coreapi/types_serviceprovider_cluster.go`):

- `ServiceProviderClusterSpec.ControlPlaneVersion.DesiredVersion *semver.Version` — **exists** (design's `spec.control_plane_version.desired_version`).
- `ServiceProviderClusterStatus.ControlPlaneVersion.ActiveVersions []ServiceProviderClusterActiveVersion` (each `{Version *semver.Version, State configv1.UpdateState, LastTransitionTime metav1.Time}`) — **exists** (design's `status.control_plane_version.active_versions`).
- `ServiceProviderClusterStatus.DesiredVersionChannels []string` — **exists**.

### The key architectural change

Previously, **every cluster independently** resolved and owned its
`Spec…DesiredVersion` via `ControlPlaneDesiredVersion`. The design replaces this
with a **fleet-coordinated** rollout: a per-y-stream-channel
`ControlPlaneVersionRollout` object computes one `bestExactVersion`, and rollout
controllers assign that version to clusters gradually (canary → rolling),
respecting SRE pins and failure budgets.

**Ownership of `ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion`
moves** from `ControlPlaneDesiredVersion` (per-cluster, immediate) to the four
assignment controllers (Forced, Normal, Initial Normal, and Minor Upgrade Normal). This is the single most important
integration risk and is addressed in §6 (Ownership and cutover).

## 2. What is net-new

This change introduces:

- The `ControlPlaneVersionRollout` type (fleet/region-wide, per y-stream channel).
- `ServiceProviderCluster.Spec.PinnedVersion.{ExactVersion,UntilExactVersion}`.
- A hardcoded rollout policy with per-channel minimum-version and per-minor timeout maps; environment tuning is a follow-up.
- A fleet-level (as opposed to per-cluster/on-demand) "best version per channel" cache.

## 3. New API types

### 3.1 `ControlPlaneVersionRollout` (fleet-scoped)

The design says it is region-wide and "the name is the y-stream channel it is
associated with" (e.g. `stable-4.21`). That is fleet scope, so it belongs in
`internal/api/fleetapi` (stored in the separate `"Fleet"` Cosmos container,
partition key = lowercased provider namespace). The resource name identifies the
channel; every rollout shares the same partition, including informer list queries.

```go
// internal/api/fleetapi/types_control_plane_version_rollout.go
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ControlPlaneVersionRollout struct {
    // The top-level name is the channel; CosmosMetadata contains the resource ID.
    coreapi.CosmosMetadata `json:"cosmosMetadata"`
    Spec   ControlPlaneVersionRolloutSpec   `json:"spec"`
    Status ControlPlaneVersionRolloutStatus `json:"status"`
}

type ControlPlaneVersionRolloutSpec struct {
    // Canonical major.minor ID and channel group; written by rollout seeding.
    Version coreapi.VersionProfile `json:"version"`
    // BestExactVersion uses recency and the channel offset, subject to the SRE
    // minimum-version floor. Conditional-update risk filtering is a follow-up.
    BestExactVersion *semver.Version `json:"bestExactVersion,omitempty"`
}

type ControlPlaneVersionRolloutStatus struct {
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    LastAssignmentTime *metav1.Time `json:"lastAssignmentTime,omitempty"`
    // keys are exact-version strings (semver.String()).
    ClusterCountByDesiredExactVersion            map[string]int64 `json:"clusterCountByDesiredExactVersion,omitempty"`
    MismatchedClusterCountByDesiredExactVersion  map[string]int64 `json:"mismatchedClusterCountByDesiredExactVersion,omitempty"`
    FailedClusterCountByDesiredExactVersion      map[string]int64 `json:"failedClusterCountByDesiredExactVersion,omitempty"`
    ClusterCountByAchievedExactVersion           map[string]int64 `json:"clusterCountByAchievedExactVersion,omitempty"`
    SuccessfulClusterCountByAchievedExactVersion map[string]int64 `json:"successfulClusterCountByAchievedExactVersion,omitempty"`
}
```

[`Spec.Version`](../../internal/api/fleetapi/types_control_plane_version_rollout.go)
holds the structured minor version and channel group: for example, `{ID: "4.21",
ChannelGroup: "stable"}` must have resource name `stable-4.21`.
[`NormalizeRolloutVersion`](../../internal/apihelpers/fleetapihelpers/rollout_version.go)
parses the name of a persisted rollout with an entirely missing/zero profile into
a deep copy. Partial profiles, noncanonical minors, unsupported channel groups and
name/profile mismatches are errors. The seeder persists the backfill with the live
ETag, preserving metadata, best version and status. Selection, membership and
status fanout use `RolloutVersionForRead` to adapt legacy identity without mutating
informer objects or taking ownership of the backfill. Decoding and ordinary
best/status/condition writes leave missing profiles visible for persisted repair.

Wiring checklist (templated on `Stamp`, see the research notes):
`types_control_plane_version_rollout.go`, `types_runtime.go` (`GetObjectKind`,
`GetObjectMeta`, `ControlPlaneVersionRolloutList`), `registry.go` (resource-type
constants), `partition.go` (resource-id builders), `ProviderNamespacePartitionKeyDeriver`, `make deepcopy`, `fleetcosmosstorage` CRUD +
`fleetcosmosstoragetesting` mock, validation, `fleetinformers` +
`fleetlisters`.

### 3.2 `ServiceProviderCluster.Spec.PinnedVersion`

```go
// internal/api/coreapi/types_serviceprovider_cluster.go
type ServiceProviderClusterSpec struct {
    ...
    // PinnedVersion, when set, forces this cluster to an SRE-chosen exact version
    // until the fleet's best version reaches UntilExactVersion.
    PinnedVersion ServiceProviderClusterPinnedVersion `json:"pinnedVersion,omitempty"`
}

type ServiceProviderClusterPinnedVersion struct {
    ExactVersion      *semver.Version `json:"exactVersion,omitempty"`
    UntilExactVersion *semver.Version `json:"untilExactVersion,omitempty"`
}
```

An unset pin is a value with nil `ExactVersion` and serializes as `{}`. This is
intentional. Consuming and clearing pins is implemented; the Admin API setter is
not yet available and remains a follow-up.

Update the `// Written by:` field annotations (see CLAUDE.md cosmos-data-flow
rule) and run `make deepcopy`.

### 3.3 `OpenShiftVersionCatalog` (regional Resources singleton)

[`OpenShiftVersionCatalog`](../../internal/api/coreapi/types_openshiftversioncatalog.go)
embeds `CosmosMetadata` and stores `Entries []OpenShiftVersionCatalogEntry`. Each
entry has `Version coreapi.VersionProfile` and `Available bool`; the publisher
owns both fields. The internal resource ID is
`/providers/microsoft.redhatopenshift/openshiftversioncatalogs/default`, partition
key `microsoft.redhatopenshift`. It lives in the regional **Resources** container,
not Fleet, and is not an ARM resource. The
[`OpenShiftVersionCatalogs()` accessor](../../internal/database/cosmosstorage/corecosmosstorage/database.go)
provides point reads and ETag-protected replacement. Empty results persist as
`entries: []`, distinct from a missing document. Frontend version GET/LIST do not
read this snapshot yet.

## 4. Rollout policy

`RolloutConfig` is passed directly from backend construction. This change has no
rollout flags or environment-config plumbing.

```go
// backend/pkg/controllers/cluster/version/rollout/config.go
type RolloutConfig struct {
    CanaryPercentage        int
    RollingPercentage       int
    MinVersionReadyDuration time.Duration
    // keyed by y-stream channel, e.g. "stable-4.21"
    MinimumVersions    map[string]semver.Version
    // keyed by minor version, e.g. "4.21"
    MaxUpgradeDuration map[string]time.Duration
}
```

Production defaults are `CanaryPercentage=6`, `RollingPercentage=12`, one hour
of readiness, and a two-hour upgrade timeout unless overridden for a minor.
`GetZStreamOffset` selects one version behind for stable and zero for other graph
channels. Nightly references seed structured rollout profiles under the shared
allowed-channel policy. The existing selector skips Cincinnati for nightly, so
nightly installs continue to use the experimental exact-version override.

Discovery uses `MinimumBackendVersion` (currently `4.20`) from
[`versionpolicy`](../../internal/versionpolicy/policy.go). Referenced minor version
and channel group pairs remain repairable below that floor. This discovery floor
is separate from the per-channel minimum exact versions used by selection.
Publication and create/changed-version validation use the separate
`MinimumPublicVersion` floor, also currently `4.20`. Retirement deletes only
unreferenced rollouts below `MinimumBackendVersion`; referenced profiles remain
repairable. See the [deprecation procedure](../ops/deprecate-openshift-version.md)
for advancing the public floor first and the backend floor only after drain.

## 5. Controllers

All nine run in the `backend` binary. Three assignment controllers are per-cluster
(use `controllerutils.NewClusterWatchingController` + `HCPClusterKey`); three are
per-`ControlPlaneVersionRollout`, keyed by the rollout channel name. Seeding uses
a homogeneous structured-profile queue; discovery and reference repair add no
controller registrations. Catalog publication and retirement each use a regional key.

[`TypedController[T]`](../../internal/controllerutils/typed_controller.go) owns the
reusable typed queue, worker/retry loop, logging, reconcile metrics, cache-sync
gating and worker `Run`. `GenericWatchingController[T]` adapts that base to the
existing `any`-based interface and resource-ID watcher helpers, including `MakeKey`
and cooldown handling.

The [periodic wrapper](../../backend/pkg/controllers/cluster/version/rollout/periodic_controller.go)
uses the typed base directly and owns producer startup/shutdown in its `Run`,
delegating worker execution to the base. The seeder constructor returns
`controllerregistry.Runnable`, not the watching-specific backend
`controllerutils.Controller`: profile keys need no resource-ID conversion or
`MakeKey` stub. It writes no child Controller bookkeeping.
The publisher also returns `controllerregistry.Runnable` and uses the typed base
directly, with rollout informer callbacks enqueueing its aggregate regional key;
it writes no child Controller bookkeeping either.
Retirement uses the periodic wrapper over the same typed base, returning
`controllerregistry.Runnable` and writing no child Controller bookkeeping.

Assignment and per-rollout controllers follow the house pattern: a syncer struct holding listers +
DB clients (interfaces), a `New…Controller` constructor, and a `SyncOnce`
implementing the read → `DeepCopy` → mutate → `equality.Semantic.DeepEqual`
skip → `Replace` (treating `IsPreconditionFailedError` as a benign no-op) loop.
Rollout counts and batch selection use **pure functions** for unit testing.
Forced assignment decisions live directly in `SyncOnce` and are tested through
the shared mock Cosmos database.

### 5.1 CS update/install controller
Already covered by `TriggerControlPlaneUpgrade` + `clusterCSVersionID`. **No new
controller** — the plan reuses the existing path. Input
`Spec…DesiredVersion` → output CS `ControlPlaneRelease`/upgrade policy.

### 5.2 Forced Cluster Desired Version Assignment (per-cluster)
- **Inputs**: `ControlPlaneVersionRollout.Spec.BestExactVersion` (for the cluster's channel), `SPC.Spec.PinnedVersion.{ExactVersion,UntilExactVersion}`.
- **Output**: `SPC.Spec.ControlPlaneVersion.DesiredVersion`.
- **Sync** (inline in `SyncOnce`):
  1. If `bestExactVersion >= pinnedVersion.untilExactVersion`: set desired = best, clear both pinned fields, return.
  2. Else if `desiredVersion != pinnedVersion.exactVersion`: set desired = pinned.exactVersion, return.
  3. Else: no-op.
- Also assigns `ExperimentalFeatures.ControlPlaneExactVersion` indefinitely when
  there is no SRE pin. An SRE pin takes precedence.
- With neither override, experimental `ZStreamUpdatePolicy=Immediate` advances
  an initialized cluster to the best newer version in its desired minor. This
  bypasses progressive gates so production e2e automatic z-stream upgrade tests
  do not depend on the canary state. It never downgrades or changes the minor.

### 5.3 Control Plane Version Status Collector (per-rollout)
- Fires when any SPC `active_versions`/`desired_version` changes or `BestExactVersion` changes. Cluster deletion marks also enqueue recomputation.
- Excludes clusters whose backing cluster is missing or marked for deletion.
  Input-change notifications use immediate enqueue, which does not increment
  retry metrics; timed rechecks keep using delayed enqueue.
- **Output**: `rollout.Status.{ClusterCountByDesiredExactVersion, MismatchedClusterCountByDesiredExactVersion, FailedClusterCountByDesiredExactVersion, ClusterCountByAchievedExactVersion, SuccessfulClusterCountByAchievedExactVersion}`.
- **Sync** (pure fn `computeRolloutStatusCounts` over the list of SPCs in the channel's minor):
  - *Desired*: count by `Spec…DesiredVersion`.
  - *Achieved*: count the oldest completed active-version entry. Partial-only
    histories are mismatched, never achieved or successful.
  - *Mismatched*: desired set but not achieved.
  - *Failed*: mismatched for longer than `maxUpgradeDuration[minor]` (requires an observation timestamp — see §8).
  - *Successful*: achieved and stable for longer than `minVersionReadyDuration`.

### 5.4 Control Plane Version Best Version Selection (per-rollout, interval)
- **Inputs**: `zStreamOffset`, Cincinnati best version for the channel, `minimumVersions[channel]`.
- **Output**: `rollout.Spec.BestExactVersion`.
- **Sync** (pure fn `selectBestExactVersion`):
  - Compute the graph best by recency and `zStreamOffset` via the shared
    `cluster/version/SelectControlPlaneVersion` helper. Conditional-update risks
    are not evaluated yet; platform/control-plane risk filtering is a follow-up.
  - `bestExactVersion = max(minimumVersions[channel], graphBest)`.

### 5.5 Z-stream Progressive Desired Version Rollout (per-rollout, interval)
- **Inputs**: `rollout.Spec.BestExactVersion`, `rollout.Status.*`, per-SPC `desired_version`/`active_versions`/`pinnedVersion`.
- **Output**: `SPC.Spec…DesiredVersion` for a bounded set of clusters.
- **Sync** (pure fns `eligibleClusters`, `rolloutDecision`):
  1. Failure budget: if `FailedClusterCount[best] > max(2, 5% of clusters desiring best)` → failure condition, return.
  2. `EligibleClusters` = non-deleting clusters in the channel's minor with a non-nil `desired < best`, and either no pin, or pinned with `untilExactVersion <= best`. Initial assignment owns nil desired versions; experimental exact-version and Immediate assignments belong to the forced controller.
  3. If no eligible → stable condition, return.
  4. Canary: if `(Mismatched+Achieved)[best] < canaryPercentage%+2` → pick N (random for now) eligible, set desired=best, return.
  5. Gate: if `Successful[best] < canaryPercentage%` → progressing condition, return.
  6. Rolling: if `(Mismatched+Achieved-Successful)[best] < rollingPercentage%` → pick N eligible, set desired=best, return. Successful clusters free window slots.
- Compute decision counts from the same cluster snapshot as eligibility so a
  lagging status collector cannot authorize another batch.
- Reserve each batch by persisting `Status.LastAssignmentTime` before cluster
  writes. Enforce at least 60 seconds between batches inside `SyncOnce`, including
  changed-resource notifications and controller restarts. ETag conflicts stop the
  assignment, and partial failures retain the reservation.

### 5.6 Rollout Seeding

Owns one homogeneous queue of `rolloutSeedKey.Version` profiles. After cache sync,
[`Run`](../../backend/pkg/controllers/cluster/version/rollout/periodic_controller.go)
starts independent HTTP discovery and cached-reference/backfill/legacy-health
cleanup producer loops. Each runs immediately and then on a five-minute ticker;
calls to the same producer never overlap. Producer failures are logged for the
next tick. Queue retry/reconcile metrics cover worker execution, not enumeration.
Targeted Cluster/SPC add and version-reference-changing update callbacks combine
the event object with its cached counterpart to enqueue normalized profiles.
Callback errors recover through periodic repair; there is no delete handler.

[`Discovery`](../../backend/pkg/controllers/cluster/version/rollout/rollout_discovery.go)
uses the [graph-data client](../../internal/cincinnati/graph_data.go) to fetch the
Cincinnati archive with a one-minute deadline and bounded in-memory validation.
Only complete validated results enqueue allowed profiles at/above the backend
floor, including in an empty region. Discovery does not select exact releases or
call Cluster Service. Its failure does not block the independent reference scan.

[`References`](../../backend/pkg/controllers/cluster/version/rollout/rollout_references.go)
include Cluster requested versions, experimental exact overrides and active
versions, plus SPC desired, pinned exact and active versions, using the parent's
channel group. Deleting resources and nightly channels remain references;
`UntilExactVersion` is only a pin comparison threshold, not a separate dependency.
Known references can be repaired even when enumeration also reports errors.

Workers live-read the rollout, create missing profiles with `Spec.Version` and a
nil best version, and recheck regional cached references before creating missing
below-floor profiles. Existing legacy profiles are backfilled using an
ETag-protected Replace; selected versions, status and metadata are preserved.
Valid structured profiles are unchanged. Create conflicts and write failures use
queue retries with a fresh live read. Pure `reconcileSeeding` computes the desired
document without I/O; `SyncOnce` gathers inputs and persists it.

The reference producer inventories all legacy rollouts, including unused
below-floor ones, for profile backfill. It deletes obsolete per-cluster seeder
Controller status only after an error-free inventory and cache observation of
valid structured rollouts for the cluster's references. Enqueueing repair is not
enough. Cleanup tolerates 404; failures wait for the next producer tick. Discovery
and reference repair share the existing seeder identity and lifecycle.

### 5.7 Initial Normal Desired Version (per-cluster)

Assigns the requested channel's best to an uninitialized cluster, excluding pins
and experimental exact-version overrides. It also backfills missing or zero
desired-version transition times on existing clusters without changing their
desired versions. The backfill starts a conservative failure clock at observation.

### 5.8 Minor Upgrade Normal Desired Version (per-cluster)

When requested and desired major/minor differ, assigns the requested channel's
best after rechecking node-pool requested and observed versions against the shared
minor-skew rules. Missing provider state or incompatible pools block assignment,
including pools still being deleted. Pins and experimental exact overrides are
owned exclusively by forced assignment. Both initial and minor assignment retry missing rollout/best data
after ten seconds and bypass progressive z-stream gates.

### 5.9 OpenShift Version Catalog Publication

[`OpenShiftVersionCatalog`](../../backend/pkg/controllers/cluster/version/rollout/version_catalog_controller.go)
uses one regional key and one worker. Rollout add/update/delete events (including
tombstones), explicit startup enqueue and a five-minute delayed requeue trigger
projection after initial Fleet cache sync. Legacy profiles are adapted at the read
boundary without persisting backfill. Pure `reconcileVersionCatalog` validates
every rollout, projects profiles at/above the public floor, sets `Available` to
`BestExactVersion != nil`, and sorts by ID then channel group. Unresolved entries
remain present and unavailable; availability is not a fleet-health gate.

The sync shell point-reads Resources `default`, creates it when absent and replaces
only changed entries with ETag concurrency. Invalid profiles, including malformed
below-floor rollouts, abort projection without replacing the previous snapshot.
Errors and conflicts use queue retries with fresh reads. Empty Fleet state produces
an empty catalog; publication does not wait for discovery's first successful pass.
Frontend GET/LIST remain on Cluster Service, so persistence has no public API effect
at this stage. See the [publication diagram](../cosmos-data-flow.md#version-catalog-publication).

### 5.10 Control Plane Version Rollout Retirement

[`ControlPlaneVersionRolloutRetirement`](../../backend/pkg/controllers/cluster/version/rollout/rollout_retirement_controller.go)
waits for its Cluster/SPC/Fleet caches, then enqueues a regional scan immediately
and on a five-minute ticker. One worker runs the scan; errors use queue retries.
The sync shell gathers cached inventories and adapts legacy profiles without
persisting backfill. Pure `reconcileRolloutRetirement` requires an error-free
reference scan and validates the entire structured rollout inventory before
returning sorted deletion candidates. Any incomplete or malformed input prevents
all deletion in that pass.

Only rollouts **below the backend floor and unreferenced** are deleted. Deleting
Cluster/SPC documents, pins, overrides and every stored active version remain
references; pin comparison thresholds do not. At/above-floor rollouts survive even
when absent from graph-data. Delete tolerates 404; other failures stop the pass and
retry, without rolling back earlier deletes. Retirement can delete an obsolete
legacy document directly, superseding backfill. A queued seeder key still needs a
current reference to recreate a missing below-floor rollout.

Deletion triggers catalog reprojection, not cluster migration or external resource
deletion. Cached references are eventually consistent, not a transactional global
lock; the manual drain prerequisite in the deprecation procedure remains necessary.

## 6. Ownership and cutover

This implementation deliberately replaces `ControlPlaneDesiredVersion`; all
nine rollout/catalog controllers run unconditionally. The earlier feature-flag proposal
was removed during review. Restoring it would reintroduce the removed owner and
is not part of this change.

`OperationClusterUpdate` observes the desired version resolved on the SPC by the
current assignment controllers. It reports incompatible forced overrides directly,
bounds unresolved version waits, and no longer reads or creates a legacy
`ControlPlaneDesiredVersion` controller document.

## 7. Testing strategy

- [Structured-profile migration tests](../../internal/apihelpers/fleetapihelpers/rollout_version_test.go)
  cover validation, deep-copy preservation, idempotence, decoding of missing
  profiles and stale-ETag protection.
- [Seeder tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_seeding_controller_test.go)
  cover event-produced profiles, independent producer timing, worker retries,
  cache-sync cancellation, live backfill, below-floor recreation guards and
  legacy-health cleanup after observed persistence.
- [Legacy writer tests](../../backend/pkg/controllers/cluster/version/rollout/legacy_writers_test.go)
  cover best/status/condition writes before backfill and stale writes after repair.
- [Publisher tests](../../backend/pkg/controllers/cluster/version/rollout/version_catalog_controller_test.go)
  cover projection, legacy read adaptation, queue behavior and periodic scheduling;
  [storage tests](../../internal/database/cosmosstorage/corecosmosstorage/version_catalog_test.go)
  cover Resources identity, partitioning and conditional replacement.
- [Retirement tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_retirement_controller_test.go)
  cover reference retention, errors blocking all deletion, legacy backfill races
  and delete failures reaching queue retries.
- **Pure decision functions** (`computeRolloutStatusCounts`, `selectBestExactVersion`, `eligibleClusters`,
  `rolloutDecision`) get exhaustive table-driven unit tests — no fakes needed.
- **`SyncOnce`** tests use the in-memory mock DB
  (`corecosmosstoragetesting.NewMockResourcesDBClient`) + slice-backed fake
  listers (`corelistertesting.Slice*Lister`, new `fleetlistertesting` fakes),
  asserting the Cosmos side-effect by reading back through the mock (including
  forced-version precedence and no-op persistence), and covering
  the `IsPreconditionFailedError` no-op path. Randomised canary/rolling selection
  is made deterministic in tests via an injectable selector (interface, not a
  closure).
- No `FRONTEND_SIMULATION_TESTING`; unit tests use fakes only.

## 8. Persisted transition times

`ServiceProviderClusterActiveVersion.LastTransitionTime` records when a version
entered its current state. `ControlPlaneActiveVersions` preserves nonzero
timestamps on unchanged entries and backfills legacy zero timestamps.
`DesiredVersionLastTransitionTime` is written by the four assignment controllers;
initial assignment also backfills it for legacy desired versions.

The status collector uses these persisted timestamps for achieved readiness and
upgrade failure durations. A restart does not reset either age. Backfilled ages
begin at observation, so historical durations are conservatively underestimated.

## 9. Implementation status and follow-ups

Implemented:

- Fleet API, validation of supported channel groups and major/minor names,
  Cosmos CRUD, partition-scoped listing, informers, listers, and mocks.
- Nine rollout/catalog controllers, backend registration under leader election, and unit tests.
- Structured `Spec.Version`, legacy read adaptation and ETag-protected backfill.
- Independent discovery/reference producers feeding one typed seeder queue, with
  below-floor reference repair and obsolete per-cluster health cleanup.
- Regional Resources catalog persistence and publication from Fleet rollout profiles.
- Retirement of below-backend-floor, unreferenced rollouts after validated inventory.
- Shared Cincinnati selection with the existing per-channel offset policy.
- Persisted transition ages and assignment cooldown reservations.
- Forced-version precedence, pinned-channel seeding, and completed-only progress.
- Cosmos field ownership and data-flow documentation.

Follow-ups:

- Frontend version GET/LIST cutover from Cluster Service to the Resources catalog.
- Filter platform/control-plane risks from Cincinnati conditional updates. The
  current graph helper selects by recency, so selected versions are not
  guaranteed to be free of conditional-update risks.
- Validate rollback targets against previously installed versions in the admin
  API. This protection is deferred; SRE pins remain immediate and bypass
  progressive rollout gates.
- Admin API contract for setting and releasing SRE pins. The consumer exists,
  but this change does not provide an operational pin-setting endpoint.
- Environment-specific configuration for rollout policy and per-channel minimum
  versions. Production values currently come from `NewDefaultRolloutConfig`.
  Experimental exact-version installs intentionally remain allowed below the
  fleet minimum for testing older releases.
