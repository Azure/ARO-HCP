# Fleet Control Plane Version Rollout — Implementation Plan

This plan maps the fleet rollout design originally authored on the
`z-stream-rollout` branch onto the current ARO-HCP codebase. It
identifies what already exists, what is net-new, and the concrete controllers,
types, config, wiring, and tests required.

> Status: the nine rollout controllers, Cosmos storage, informers, structured-profile
> migration, backend wiring, and Admin API pin setter (PR #7162) are implemented. The rollout controllers
> run unconditionally. Production policy is hardcoded; risk filtering and
> environment configuration remain follow-ups.

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
    // Canonical major.minor ID and channel group matching the resource name.
    Version coreapi.VersionProfile `json:"version"`
    // BestExactVersion uses recency and the channel offset, subject to the SRE
    // minimum-version floor. Conditional-update risk filtering is a follow-up.
    BestExactVersion *semver.Version `json:"bestExactVersion,omitempty"`
}

type ControlPlaneVersionRolloutStatus struct {
    // Unique referencing parent clusters, including deleting clusters.
    // Nil means no inventory count has been observed and published.
    // Zero means the last successfully published inventory found no references.
    // Collection errors leave all prior status unchanged for queue retry.
    ReferencingClusterCount *int64 `json:"referencingClusterCount,omitempty"`
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

Wiring checklist (templated on `Stamp`, see the research notes):
`types_control_plane_version_rollout.go`, `types_runtime.go` (`GetObjectKind`,
`GetObjectMeta`, `ControlPlaneVersionRolloutList`), `registry.go` (resource-type
constants), `partition.go` (resource-id builders), `ProviderNamespacePartitionKeyDeriver`, `make deepcopy`, `fleetcosmosstorage` CRUD +
`fleetcosmosstoragetesting` mock, validation, `fleetinformers` +
`fleetlisters`.

`Spec.Version` is populated on creation: `stable-4.21` has `ID: "4.21"` and
`ChannelGroup: "stable"`. Create/update validation requires agreement with the
name. Selection/assignment consumers parse channel names; retirement uses the
profile as its target identity.

Shared Cosmos conversion fills absent/zero legacy profiles from valid names
before Get/List results reach callers or informer caches, without a storage write.
Partial or mismatched profiles are left for validation to reject. Ordinary rollout
replacements persist the normalized profile alongside their existing changes.

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
intentional. Consuming and clearing pins is implemented; the Admin API setter
is implemented via the single-cluster `POST /controlplaneversionpin` and regional
`POST /admin/v1/versionrollouts/{channel}/controlplaneversionpin` endpoints (see
`admin/server/handlers/hcp/controlplaneversionpin.go` and `fleetcontrolplaneversionpin.go`).

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
channels. Nightly assignments come from the experimental exact-version override.
Independent Cincinnati discovery seeds rollouts for allowed groups including
`nightly`.

The backend lifecycle floor, `coreapi.MinimumBackendVersion`, is hardcoded to `4.20`,
comparing major/minor only. It bounds prospective Cincinnati seeding and permits
retirement below the floor when the status collector reports zero referencing
clusters. It is independent of admission version policy and the per-channel
exact-version `MinimumVersions` policy governing best-version selection.
Raising the lifecycle floor requires operationally draining
the retired minor and its references, including coordinating new cluster requests
and pins; retirement consumes cached status observations.

## 5. Controllers

All nine rollout controllers run in the `backend` binary. Four are per-cluster (use
`controllerutils.NewClusterWatchingController` + `HCPClusterKey`); five are
per-`ControlPlaneVersionRollout`, using the existing generic fleet watching
controller keyed by channel name with informer resync. Cincinnati seeding also
uses an external-discovery periodic path. Profile migration runs in a separate
controller.

The rollout syncer interface requires `SyncOnce`. Its watching wrapper forwards
an optional `CooldownChecker`, allowing profile migration to retain its 5m queue
cooldown. The five rollout syncers rely on events and resync; progressive assignment
enforces its persisted 60s batch cooldown inside `SyncOnce` so it also covers
changed events and process restarts.

The [cluster registry](../../backend/pkg/controllers/cluster/registration.go) assigns
one worker each for `ControlPlaneVersionCincinnatiSeeding` and
`ControlPlaneVersionRolloutRetirement`. The backend registry totals 117 launches
(116 with `HasRealFPA=false`); the per-cluster seeder uses 20 workers.
Both lifecycle controllers use registration-tracked cache sync under backend leader election.

Existing selection/assignment controllers follow the house pattern: a syncer struct holding listers +
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
- Watches rollouts and maps Cluster/SPC add, old-and-new update, and
  delete/tombstone events to dependent channel keys through `collectRolloutReferences`.
  All handlers use 5m resync, including unchanged input updates. Known dependencies
  enqueue even when some references are unresolved; resync retries the mapping.
  Input notifications use immediate enqueue without incrementing retry metrics.
- **Reference output**: `rollout.Status.ReferencingClusterCount` counts the union of
  unique parent clusters referencing `Spec.Version` through requested versions,
  experimental exact overrides, all Cluster active versions, SPC desired and all
  active versions, and `PinnedVersion.ExactVersion`, using the parent's channel
  group. Deleting clusters remain references. Pin release uses the pinned minor's
  rollout; `UntilExactVersion` is its release threshold.
- The collector fetches the Cluster and SPC slices once per reconcile and uses
  those same slices for reference collection and progress membership. Successful
  collection publishes the reference count and progress maps together using
  optimistic concurrency. Nil means no inventory count has been observed and
  published; zero means the last successfully published inventory found no references.
  Missing parent identity/channel information, malformed references, lister errors,
  or progress-membership errors return immediately with all prior rollout status
  unchanged, including the reference count and progress maps, for workqueue retry.
- **Progress output**: `rollout.Status.{ClusterCountByDesiredExactVersion, MismatchedClusterCountByDesiredExactVersion, FailedClusterCountByDesiredExactVersion, ClusterCountByAchievedExactVersion, SuccessfulClusterCountByAchievedExactVersion}`.
- Progress membership uses the parent's channel group and desired minor, falling
  back to the oldest completed active minor. Missing or deleting backing clusters
  are excluded from these maps.
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

### 5.6 Rollout Seeding (per-cluster)

Creates a rollout with `Spec.Version` populated for the customer's requested
channel and, when pinned, the pinned minor's channel. Best version and status
start unset. Existing rollout contents are preserved. Nightly and deleting
clusters are skipped.

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

### 5.9 Rollout Version Migration (per-rollout)

The separate backend `CosmosRolloutVersionMigration` controller uses
`NewControlPlaneVersionRolloutWatchingController`, five workers and a 5m
resync/cooldown. It has no subscription dependency; the subscription-scoped
`CosmosMigration` remains unchanged. It live-reads and replaces each rollout,
including already-structured documents, to persist the normalized profile.
ETag conflicts/precondition failures re-read and retry within three attempts per
reconcile, preserving selection and status; not-found is a no-op. Only a
successful replacement marks that key complete in memory for the process.

An older writer can subsequently drop the unknown field. Read compatibility
still works, but migration does not rewrite a completed key again in that
process; an ordinary replacement or process restart can persist it again.

### 5.10 Cincinnati Seeding (per-rollout)

`ControlPlaneVersionCincinnatiSeeding` uses the generic rollout watcher with 5m
resync. Shared add/update/delete handlers route events through resource-interest
filtering and depth-limited key resolution. The delete handler unwraps
`cache.DeletedFinalStateUnknown`, ignores unusable metadata/resource IDs, and
enqueues accepted removals immediately as changed events for repair. Its `Run`
waits for the tracked rollout cache, then runs external discovery with
`wait.JitterUntilWithContext` (5-minute period, 0.1 jitter, sliding=true): immediately,
then 5-5.5 minutes after each pass completes. Discovery enqueues channel keys;
the generic worker reconciles. Cancellation stops the discovery loop, discovery
failures wait for the next pass, and worker errors follow normal queue retry.

The constructor receives `cincinnati.VersionProfileSource`, whose
`VersionProfiles(context.Context)` method supplies discovery profiles. The root
command constructs `cincinnati.NewGraphDataClient()` and injects it through the
required `BackendOptions.VersionProfileSource`, `ControllerContext`, and the
registration adapter. This application-boundary dependency lets tests supply
deterministic discovery while production uses the archive client.

The graph-data archive client has a one-minute deadline and validates bounded
in-memory input before returning profiles. Unsupported upstream channel groups
are skipped; allowed experimental groups, including nightly, are accepted.
Discovered profiles at/above `coreapi.MinimumBackendVersion` are normalized,
deduplicated and enqueued. `SyncOnce` rechecks the floor before the cached rollout
lister's `Get`, creating the target only if absent with `Spec.Version` populated and
best/status unset. It preserves existing documents and treats create conflicts as
successful reconciliation. Discovery proactively seeds channels for future clusters.

### 5.11 Rollout Retirement (per-rollout)

`ControlPlaneVersionRolloutRetirement` uses the generic target-rollout watcher
with 5m resync. Startup waits for the tracked rollout cache.

Each reconcile reads the cached target's `Spec.Version` and
`Status.ReferencingClusterCount`. A populated profile below
`coreapi.MinimumBackendVersion` and an explicit zero count permit conditional
soft deletion from Fleet storage via `DeleteWithETag`, passing the cached rollout's
`CosmosETag` used to evaluate eligibility. Nil or nonzero counts retain the target;
an empty profile waits for another notification. An already-absent or already
soft-deleted target completes reconciliation successfully.
Supported zero-member rollouts and their state are retained. Reference inventory
and dependency watches belong to the status collector (§5.3).

Fleet rollouts use the standard typed `ValidatingResourceCRUD`. Both shared typed
CRUD interfaces (`ResourceCRUD` and `ValidatingResourceCRUD`) embed
`ConditionalDeleter`, exposing `DeleteWithETag` to callers. Validating wrappers
forward the resource name and expected ETag; layered wrappers run the operation
through their configured CRUD layer. Shared mock CRUD performs the ETag check,
soft-delete write, and change-feed recording under the store mutex, giving core,
Fleet, and kube-applier stores the same atomic conditional-delete behavior.

Storage compares the supplied ETag with the live document's ETag before setting
soft-delete fields, then uses that same supplied ETag as `IfMatchEtag` on the
`ReplaceItem` write. A mismatch at either step yields a precondition failure;
retirement treats it as a benign no-op and reevaluates on a later rollout watch
event or resync. This protects newer persisted rollout state, including an updated
reference count, from deletion based on an older cached version. It is not a
transactional check of Cluster/SPC references: retirement consumes asynchronously
published status, and collection errors preserve the last successfully published
status (§5.3). Operational coordination of new references is still required when
raising the lifecycle floor.

## 6. Ownership and cutover

All nine rollout controllers run unconditionally.

`OperationClusterUpdate` observes the desired version resolved on the SPC by the
current assignment controllers. It reports incompatible forced overrides directly,
bounds unresolved version waits, and no longer reads or creates a legacy
`ControlPlaneDesiredVersion` controller document.

## 7. Testing strategy

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
- Backend launch tests inject a `VersionProfileSource`. The metrics launch test
  uses an empty source to exercise startup and metric collection while keeping
  discovery free of external DNS/HTTP requests and prospective rollout creation.
- Shared watcher tests cover direct deletes and tombstones, resource-interest
  filtering, and parent-depth limits. CRUD tests cover conditional-delete forwarding
  through validating/layered wrappers and mock-store ETag behavior.

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
- Nine rollout controllers, including Cincinnati seeding and retirement,
  backend registration under leader election, and unit tests.
- Unique-parent reference counts published by the status collector and consumed
  by rollout retirement, with nil representing a count not yet observed and
  published. Collection errors preserve all prior rollout status for queue retry.
- Conditional rollout retirement using the cached eligibility ETag for the live
  ETag comparison and soft-delete replacement; precondition failures wait for
  watch/resync reevaluation without deleting newer persisted rollout state.
- Structured rollout version profiles, shared read compatibility, and the separate
  `CosmosRolloutVersionMigration` controller for persisted backfill.
- Shared Cincinnati selection with the existing per-channel offset policy.
- Persisted transition ages and assignment cooldown reservations.
- Forced-version precedence, pinned-channel seeding, and completed-only progress.
- Cosmos field ownership and data-flow documentation.
- Admin API `POST /controlplaneversionpin` setter/clearer. Setting a pin in the
  cluster's release line can hold or advance the latest observed version.
  Rollback below that version requires the immediately previous distinct
  `Completed` entry in
  `ServiceProviderCluster.Status.ControlPlaneVersionHistory`, mirrored from
  `HostedCluster.Status.ControlPlaneVersion.History`.
  Without mirrored history, the pin is accepted because its direction cannot
  be classified from observation. Partial attempts are not rollback targets.
  The control-plane history field
  is present in 4.22 and was backported to
  [release-4.20](https://github.com/openshift/hypershift/pull/9055) and
  [release-4.21](https://github.com/openshift/hypershift/pull/9054); older
  deployed operator builds may lack it. Guest version history is not a
  substitute for rollback validation. The handler checks available observed
  history rather than enforcing a minimum hosted-cluster version.

- Regional Admin API `POST /admin/v1/versionrollouts/{channel}/controlplaneversionpin`
  sets/clears existing per-cluster pins using rollout channel membership. All pin
  changes are validated before writes; identical pins are no-ops. Independent
  ETag writes return counts and per-cluster failures on partial success. This
  endpoint creates no persistent policy for future clusters.

Follow-ups:

- Filter platform/control-plane risks from Cincinnati conditional updates. The
  current graph helper selects by recency, so selected versions are not
  guaranteed to be free of conditional-update risks.
- Environment-specific configuration for rollout policy and per-channel minimum
  versions. Production values currently come from `NewDefaultRolloutConfig`.
  Experimental exact-version installs intentionally remain allowed below the
  fleet minimum for testing older releases.
