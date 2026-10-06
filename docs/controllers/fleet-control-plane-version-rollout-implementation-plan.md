# Fleet Control Plane Version Rollout — Implementation Plan

This plan maps the fleet rollout design originally authored on the
`z-stream-rollout` branch onto the current ARO-HCP codebase. It
identifies what already exists, what is net-new, and the concrete controllers,
types, config, wiring, and tests required.

> Status: the seven rollout controllers plus catalog publication and retirement,
> the separate CosmosRolloutVersionMigration controller, Cosmos storage, informers,
> and backend wiring
> are implemented. They run unconditionally. Production policy is hardcoded;
> risk filtering, environment configuration, and the Admin API pin setter remain follow-ups.

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
    // Canonical major.minor ID and channel group; seeded on creation, normalized by storage reads.
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
ChannelGroup: "stable"}` must have resource name `stable-4.21`, the corresponding
Cincinnati channel name.
Shared [Cosmos read conversion](../../internal/database/cosmosstorage/cosmosstorageutils/convert_generic.go)
fills absent profiles from legacy channel names, so ordinary legacy reads need not
wait for migration. Reads perform no persistence. Consumers rely on valid stored
data from previous controllers rather than repeating write validation. Best selection,
status collection and progressive rollout return nil for an absent (zero-value)
profile; status fanout skips it. Later watch events or periodic resync resume work.
Catalog defers the entire projection without error if any profile is absent,
preserving the snapshot. Retirement skips only the target with an absent
profile; other rollout keys proceed independently. Initial, minor-upgrade and
forced assignment consume only `Spec.BestExactVersion`, with no profile checks.
CosmosRolloutVersionMigration
watches individual rollouts and persists normalized representations with ETag
protection, preserving metadata, best version and status. Create and Replace retain
write validation. Profile consumers use structured `Spec.Version` directly; seeding
does not own migration.

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

The separate `MinimumPublicVersion` and `MinimumBackendVersion` constants in
[`versionpolicy`](../../internal/versionpolicy/policy.go) are both `4.20`.
The public floor controls publication/admission; the backend floor controls
discovery and unreferenced-rollout retirement. Referenced minor version and channel
group pairs remain repairable below the backend floor. See the
[deprecation procedure](../ops/deprecate-openshift-version.md) for advancing them.

## 5. Controllers

All nine rollout/catalog controllers plus the separate CosmosRolloutVersionMigration
run in the `backend` binary. Three assignment controllers are per-cluster
(use `controllerutils.NewClusterWatchingController` + `HCPClusterKey`); six, including
seeding, retirement and migration, use the existing per-rollout watcher and
`ControlPlaneVersionRolloutKey.YStreamChannel`. Catalog publication uses one regional
singleton key.

The existing `GenericWatchingController[T]` owns queues, workers, retries, logging,
reconcile metrics, cache-sync gating and resource-ID mapping. The catalog syncer's
`MakeKey` maps every rollout resource ID to `versionCatalogKey{}`. Seeding adds only
a cache-gated Cincinnati discovery loop in its concrete `Run`, delegating worker
execution to the generic watcher. Seeding and retirement share Cluster/SPC
dependency handlers in
[`rollout_reference_watches.go`](../../backend/pkg/controllers/cluster/version/rollout/rollout_reference_watches.go).
None of these three controllers writes child Controller bookkeeping.

Assignment and per-rollout controllers other than migration follow the house pattern: a syncer struct holding listers +
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

Uses the existing rollout watcher with channel keys, rollout add/update/resync
notifications and an additional rollout delete handler. After cache sync, its concrete
[`Run`](../../backend/pkg/controllers/cluster/version/rollout/rollout_seeding_controller.go)
starts only HTTP discovery, immediately and then on a fixed five-minute ticker,
not a delay after completion. Slow passes never overlap and missed ticks may coalesce;
discovery failures are logged for the next tick. Queue retry/reconcile metrics
cover worker execution.

Shared Cluster/SPC handlers map add/delete and both old/new objects on every update,
including unchanged five-minute resyncs, to channel keys. Cached counterpart lookups
are allowed for dependency mapping only: callbacks neither filter eligibility nor
persist state. There are no field-change filters; callback errors recover on resync.

Workers live-read the rollout, create missing discovered profiles at/above the
backend floor, and recheck the regional cached references before creating missing
below-floor profiles. References include requested, desired, pinned, override and
active versions, including deleting resources and nightly channels; pin comparison
thresholds are excluded. Existing rollouts are a no-op and left unchanged;
workers only create missing documents.
Create conflicts and write failures use queue retries with a fresh live read.

After an error-free inventory, worker `cleanupLegacyStatus` removes obsolete
per-cluster seeder Controller status for clusters referencing that worker's channel,
only once the cache shows all their referenced rollouts. Profile presence and
migration persistence are not cleanup gates. Missing cached rollouts and cleanup
failures return errors for queue retry. Seeding does not migrate legacy rollouts.

Graph-data typically has no nightly channel definitions, and the existing
Cincinnati selector does not resolve nightly releases. The generic catalog honors
any selected nightly rollout, with candidate/nightly visibility gated by the shared
experimental-release AFEC rule. Nightly exact-pin admission remains unchanged.

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

### 5.9 Cosmos Rollout Version Migration (per-rollout)

[`CosmosRolloutVersionMigration`](../../backend/pkg/controllers/cosmosmigration/rollout_version_migration.go)
is registered separately with five workers and five-minute cooldown/resync. It uses
the existing `NewControlPlaneVersionRolloutWatchingController`, whose generic `Run`
waits for the rollout cache; there is no custom `Run`, polling loop or Fleet sweep.
Rollout add/update/resync events enqueue channel keys without subscription or cluster
dependencies, including unused rollouts. The original subscription `CosmosMigration`
retains its Resources/kube-applier migration behavior and does not access Fleet.

Each key always performs a live Get followed by validated
`Replace(old.DeepCopy(), old, nil)`, even for an already-structured profile, until
one Replace succeeds in that process. Conflict/precondition failures re-read and
retry up to three attempts; remaining errors use queue retries. NotFound at Get or
Replace, including soft-deleted documents, is a benign no-op without marking completion or recreating
the document. Successful keys remain complete despite later events or resyncs;
other keys progress independently. A later legacy write is normalized on read but
needs an ordinary write or migration after restart to persist again. The one-shot
integration helper lists rollouts and delegates to the same `SyncOnce`.

### 5.10 Rollout Retirement (per-rollout)

Uses the existing rollout watcher plus the same Cluster/SPC dependency handlers as
seeding, all with five-minute resync. It has no timer or regional scan key. Each
worker reads its target rollout and current Cluster/SPC references from caches;
missing targets or absent target profiles return nil without blocking other keys.
Any reference-inventory error prevents deletion. Only a target below the backend
floor with no current references is deleted, tolerating 404. Events are dependency
hints, not authorization to delete; queued work rechecks current cached state.

### 5.11 Version Catalog (regional singleton)

Uses `GenericWatchingController` with `MakeKey` returning `versionCatalogKey{}` for
every rollout resource ID. A bootstrap enqueue publishes even from an empty
informer after initial cache sync. Rollout add/update/delete notifications, including
tombstones and unchanged five-minute resyncs, enqueue the same key. There is no
delayed recurring requeue. Reconciliation projects profiles at/above the public floor,
with availability determined by non-nil best version, and persists only changed
content. Any absent profile defers the entire projection without error; queue errors
use normal retries.

## 6. Ownership and cutover

The assignment controllers own desired-version writes; all nine rollout/catalog
controllers and the separate rollout migration controller run unconditionally.

`OperationClusterUpdate` observes the desired version resolved on the SPC by the
current assignment controllers. It reports incompatible forced overrides directly,
bounds unresolved version waits, and no longer reads or creates a legacy
`ControlPlaneDesiredVersion` controller document.

## 7. Testing strategy

- Storage and [CosmosRolloutVersionMigration tests](../../backend/pkg/controllers/cosmosmigration/fleet_migration_test.go) own raw legacy normalization and persistence
  coverage. The [writer regression](../../backend/pkg/controllers/cluster/version/rollout/legacy_writers_test.go)
  retains raw legacy JSON to verify normalized reads, preserved profiles and stale-ETag protection.
- [Seeder tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_seeding_controller_test.go)
  cover cached event-produced channel keys, discovery timing independent of worker
  retries, cache-sync cancellation, preservation of existing state, below-floor recreation guards
  and obsolete Controller-status cleanup after observing referenced rollouts.
- [Catalog](../../backend/pkg/controllers/cluster/version/rollout/version_catalog_controller_test.go)
  and [retirement tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_retirement_controller_test.go)
  cover structured reconciliation, catalog bootstrap/event/resync handling, whole
  catalog deferral for absent profiles, and independent per-rollout retirement.
- [Reference-watch tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_reference_watches_test.go)
  cover both old/new dependencies on every update, add/delete, resync, cached
  counterpart lookups and partial reference errors without eligibility filtering.
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
- Nine rollout/catalog controllers, backend registration under leader election,
  and unit tests, including structured-profile consumption and discovery lifecycle.
- Separate CosmosRolloutVersionMigration watcher, cache-gated registration and
  per-key once-successful persistence; subscription CosmosMigration is unchanged.
- Shared Cincinnati selection with the existing per-channel offset policy.
- Persisted transition ages and assignment cooldown reservations.
- Forced-version precedence, pinned-channel seeding, and completed-only progress.
- Cosmos field ownership and data-flow documentation.

Follow-ups:

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
