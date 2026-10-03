# Fleet Control Plane Version Rollout — Implementation Plan

This plan maps the fleet rollout design originally authored on the
`z-stream-rollout` branch onto the current ARO-HCP codebase. It
identifies what already exists, what is net-new, and the concrete controllers,
types, config, wiring, and tests required.

> Status: the seven rollout controllers plus catalog publication and retirement,
> Cosmos storage, informers, and backend wiring
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
ChannelGroup: "stable"}` must have resource name `stable-4.21`, the corresponding
Cincinnati channel name.
[`NormalizeRolloutVersion`](../../internal/apihelpers/fleetapihelpers/rollout_version.go)
parses the name of a persisted rollout with an entirely missing/zero profile into
a deep copy. Partial profiles and name/profile mismatches are errors. The seeder
persists the backfill with the live ETag, preserving metadata, best version and
status. Catalog publication and retirement use the same adapter at their read
boundaries, then reconcile structured profiles. Decoding leaves missing profiles
visible for persisted repair. See the
[migration and concurrency tests](../../internal/apihelpers/fleetapihelpers/rollout_version_test.go).

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

All nine run in the `backend` binary. Three assignment controllers are per-cluster
(use `controllerutils.NewClusterWatchingController` + `HCPClusterKey`); three are
per-`ControlPlaneVersionRollout`, keyed by the rollout channel name. Seeding uses
a structured-profile queue; catalog publication and retirement use regional keys.

`TypedController[T]` in `internal/controllerutils/typed_controller.go` owns the
reusable typed queue, worker/retry loop, logging, reconcile metrics, cache-sync
gating and worker `Run`. `GenericWatchingController[T]` is the adapter on that
base that preserves the existing `any`-based interface and resource-ID watcher
helpers, including `MakeKey` and cooldown handling.

The [periodic wrapper](../../backend/pkg/controllers/cluster/version/rollout/periodic_controller.go)
uses `TypedController[T]` directly and owns producer startup/shutdown in its `Run`,
delegating worker execution to the base. Seeding and retirement constructors return
`controllerregistry.Runnable`, not the backend `controllerutils.Controller`, whose
contract requires watching. The catalog publisher also uses the typed base
directly, with explicitly wired informer callbacks enqueueing its aggregate
regional key. This separates queue execution from event-to-key mapping: producer
and aggregate keys need no resource-ID conversion or panic-only `MakeKey` stub.
None of these three controllers writes child Controller bookkeeping; producer
timing, retry/metrics behavior and reconciliation policies are unchanged.

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
cleanup producer loops. Each runs immediately and then at five-minute intervals;
producer failures are logged for the next interval. Queue retry/reconcile metrics
cover worker execution. Targeted Cluster/SPC event callbacks use the event object
and cached counterpart to produce normalized profiles; errors recover through
periodic repair.

Workers live-read the rollout, create missing discovered profiles at/above the
backend floor, and recheck the regional cached references before creating missing
below-floor profiles. References include requested, desired, pinned, override and
active versions, including deleting resources and nightly channels; pin comparison
thresholds are excluded. Existing legacy profiles are backfilled using an
ETag-protected Replace; selected versions, status and metadata are preserved.
Create conflicts and write failures use queue retries with a fresh live read.

The reference producer inventories all legacy rollouts, including unused
below-floor ones, for profile backfill. It removes obsolete per-cluster seeder
Controller status only after an error-free inventory and cache observation of
valid structured rollouts for the cluster's references. Cleanup failures wait for
the next producer interval. Discovery and reference repair share the seeder's
controller identity and lifecycle.

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

## 6. Ownership and cutover

The assignment controllers own desired-version writes; all nine rollout/catalog
controllers run unconditionally.

`OperationClusterUpdate` observes the desired version resolved on the SPC by the
current assignment controllers. It reports incompatible forced overrides directly,
bounds unresolved version waits, and no longer reads or creates a legacy
`ControlPlaneDesiredVersion` controller document.

## 7. Testing strategy

- [Structured-profile migration tests](../../internal/apihelpers/fleetapihelpers/rollout_version_test.go)
  cover name/profile validation, deep-copy preservation, idempotence, decoding of
  missing profiles and stale-ETag protection.
- [Seeder tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_seeding_controller_test.go)
  cover cached event-produced profiles, independent producer timing and worker
  retries, cache-sync cancellation, live backfill, below-floor recreation guards
  and legacy-health cleanup after observed persistence.
- [Catalog](../../backend/pkg/controllers/cluster/version/rollout/version_catalog_controller_test.go)
  and [retirement tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_retirement_controller_test.go)
  cover structured reconciliation and legacy read-boundary adaptation.
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
  and unit tests, including structured-profile migration and producer lifecycle.
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
