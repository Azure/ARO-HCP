# Fleet Control Plane Version Rollout — Implementation Plan

This plan maps the design in
[`fleet-control-plane-version-rollout.md`](./fleet-control-plane-version-rollout.md)
(authored on the `z-stream-rollout` branch) onto the current ARO-HCP codebase. It
identifies what already exists, what is net-new, and the concrete controllers,
types, config, wiring, and tests required.

> Status: living document. The controller logic and unit tests described in the
> "This PR" scope below are implemented under
> `backend/pkg/controllers/versionrollout/`. Full Cosmos/informer/backend wiring
> and SRE config plumbing are tracked as follow-up phases.

## 1. Background: what already exists

The **per-cluster** control-plane version pipeline is already built and running in
the backend binary:

| Concern | Existing code | Reads | Writes |
|---|---|---|---|
| Resolve a concrete z-stream for a cluster from its desired minor + channel, using the Cincinnati upgrade graph | `backend/pkg/controllers/cluster/version/control_plane_desired_version_controller.go` (`ControlPlaneDesiredVersion`) | `HCPOpenShiftCluster.CustomerProperties.Version.{ID,ChannelGroup}`, `ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions`, Cincinnati | `ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion` |
| Push the desired version to cluster-service (post a `ControlPlaneUpgradePolicy`) | `backend/pkg/controllers/cluster/version/trigger_control_plane_upgrade_controller.go` (`TriggerControlPlaneUpgrade`) | `Spec…DesiredVersion` vs `Status…ActiveVersions[0]` | CS `ControlPlaneUpgradePolicy` |
| Mirror observed HostedCluster versions back onto the ServiceProviderCluster | `backend/pkg/controllers/cluster/version/control_plane_active_version_controller.go` (`ControlPlaneActiveVersions`) | HostedCluster `status.controlPlaneVersion.history` / `status.version.desired.channels` | `ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions`, `Status.DesiredVersionChannels` |
| Query the upgrade graph | `internal/cincinnati/{client,graph_client,cache,utils}.go` — `GraphClient.ChannelExists`, `Client.GetUpdates`, plus `FindBestVersionInMinor` in the desired-version controller | — | — |
| Convert a semver into a CS version id (`4.21` → `openshift-v4.21.20`) | `internal/ocm/client.go` `NewOpenShiftVersionXYZ`, `internal/ocm/convert.go` `clusterCSVersionID` | `Spec…DesiredVersion` | CS cluster/nodepool version id |

Relevant existing API types (`internal/api/coreapi/types_serviceprovider_cluster.go`):

- `ServiceProviderClusterSpec.ControlPlaneVersion.DesiredVersion *semver.Version` — **exists** (design's `spec.control_plane_version.desired_version`).
- `ServiceProviderClusterStatus.ControlPlaneVersion.ActiveVersions []HCPClusterActiveVersion` (each `{Version *semver.Version, State configv1.UpdateState}`) — **exists** (design's `status.control_plane_version.active_versions`).
- `ServiceProviderClusterStatus.DesiredVersionChannels []string` — **exists**.

### The key architectural change

Today, **every cluster independently** resolves and owns its
`Spec…DesiredVersion` via `ControlPlaneDesiredVersion`. The design replaces this
with a **fleet-coordinated** rollout: a per-y-stream-channel
`ControlPlaneVersionRollout` object computes one `bestExactVersion`, and rollout
controllers assign that version to clusters gradually (canary → rolling),
respecting SRE pins and failure budgets.

**Ownership of `ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion`
moves** from `ControlPlaneDesiredVersion` (per-cluster, immediate) to the two new
assignment controllers (Forced + Normal). This is the single most important
integration risk and is addressed in §6 (Coexistence & cutover).

## 2. What is net-new

Confirmed by searching the whole tree — none of these exist today:

- The `ControlPlaneVersionRollout` type (fleet/region-wide, per y-stream channel).
- `ServiceProviderCluster.Spec.PinnedVersion.{ExactVersion,UntilExactVersion}`.
- SRE-tunable rollout config: `minimumVersions[channel]`, `maxUpgradeDuration[minor]`, `minVersionReadyDuration`, `canaryPercentage`, `rollingPercentage`, `zStreamOffset`.
- A fleet-level (as opposed to per-cluster/on-demand) "best version per channel" cache.

## 3. New API types

### 3.1 `ControlPlaneVersionRollout` (fleet-scoped)

The design says it is region-wide and "the name is the y-stream channel it is
associated with" (e.g. `stable-4.21`). That is fleet scope, so it belongs in
`internal/api/fleetapi` (stored in the separate `"Fleet"` Cosmos container,
partition key = its own top-level resource name), templated on `fleetapi.Stamp`.

```go
// internal/api/fleetapi/types_control_plane_version_rollout.go
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ControlPlaneVersionRollout struct {
    // PartitionKey / top-level resource name is the y-stream channel, e.g. "stable-4.21".
    coreapi.CosmosMetadata `json:"cosmosMetadata"`
    ResourceID *azcorearm.ResourceID `json:"resourceId,omitempty"`
    Spec   ControlPlaneVersionRolloutSpec   `json:"spec"`
    Status ControlPlaneVersionRolloutStatus `json:"status"`
}

type ControlPlaneVersionRolloutSpec struct {
    // BestExactVersion is the most recent z-stream without a platform+controlplane
    // risk in this y-stream channel, offset by zStreamOffset from the latest.
    BestExactVersion *semver.Version `json:"bestExactVersion,omitempty"`
}

type ControlPlaneVersionRolloutStatus struct {
    Conditions []metav1.Condition `json:"conditions,omitempty"`
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
constants), `partition.go` (resource-id builders), `FleetPartitionKeyDeriver`
type switch, `make deepcopy`, `fleetcosmosstorage` CRUD +
`fleetcosmosstoragetesting` mock, validation, `fleetinformers` +
`fleetlisters`.

### 3.2 `ServiceProviderCluster.Spec.PinnedVersion`

```go
// internal/api/coreapi/types_serviceprovider_cluster.go
type ServiceProviderClusterSpec struct {
    ...
    // PinnedVersion, when set, forces this cluster to an SRE-chosen exact version
    // until the fleet's best version reaches UntilExactVersion.
    PinnedVersion *ServiceProviderClusterPinnedVersion `json:"pinnedVersion,omitempty"`
}

type ServiceProviderClusterPinnedVersion struct {
    ExactVersion      *semver.Version `json:"exactVersion,omitempty"`
    UntilExactVersion *semver.Version `json:"untilExactVersion,omitempty"`
}
```

Update the `// Written by:` field annotations (see CLAUDE.md cosmos-data-flow
rule) and run `make deepcopy`.

## 4. SRE-tunable config

Model the design's `Code.ControlPlaneUpgradeController.*` as a plain Go config
struct threaded from backend cobra flags, following the existing
`backups.BackupConfig` precedent (`backend/cmd/root.go`).

```go
// backend/pkg/controllers/versionrollout/config.go
type RolloutConfig struct {
    ZStreamOffset           int
    CanaryPercentage        int
    RollingPercentage       int
    MinVersionReadyDuration time.Duration
    // keyed by y-stream channel, e.g. "stable-4.21"
    MinimumVersions    map[string]semver.Version
    // keyed by minor version, e.g. "4.21"
    MaxUpgradeDuration map[string]time.Duration
}
```

Defaults per the design: `ZStreamOffset=2`, `CanaryPercentage=5`,
`RollingPercentage=5`. Later phases add config-schema + `config/config.yaml`
plumbing; this PR passes a value in code.

## 5. Controllers

All run in the `backend` binary. Two are per-cluster (use
`controllerutils.NewClusterWatchingController` + `HCPClusterKey`); three are
per-`ControlPlaneVersionRollout` (use a new fleet watching controller keyed by
the rollout channel name, or `genericWatchingController[T]` on the rollout
resource type + interval resync).

Every controller follows the house pattern: a syncer struct holding listers +
DB clients (interfaces), a `New…Controller` constructor, and a `SyncOnce`
implementing the read → `DeepCopy` → mutate → `equality.Semantic.DeepEqual`
skip → `Replace` (treating `IsPreconditionFailedError` as a benign no-op) loop.
All decision logic is factored into **pure functions** for unit testing.

### 5.1 CS update/install controller
Already covered by `TriggerControlPlaneUpgrade` + `clusterCSVersionID`. **No new
controller** — the plan reuses the existing path. Input
`Spec…DesiredVersion` → output CS `ControlPlaneRelease`/upgrade policy.

### 5.2 Forced Cluster Desired Version Assignment (per-cluster)
- **Inputs**: `ControlPlaneVersionRollout.Spec.BestExactVersion` (for the cluster's channel), `SPC.Spec.PinnedVersion.{ExactVersion,UntilExactVersion}`.
- **Output**: `SPC.Spec.ControlPlaneVersion.DesiredVersion`.
- **Sync** (pure fn `computeForcedDesiredVersion`):
  1. If `bestExactVersion >= pinnedVersion.untilExactVersion`: set desired = best, clear both pinned fields, return.
  2. Else if `desiredVersion != pinnedVersion.exactVersion`: set desired = pinned.exactVersion (the design says "best" here; see Open Questions §8), return.
  3. Else: no-op.
- Only acts on clusters that have a `PinnedVersion`.

### 5.3 Control Plane Version Status Collector (per-rollout)
- Fires when any SPC `active_versions`/`desired_version` changes or `BestExactVersion` changes.
- **Output**: `rollout.Status.{ClusterCountByDesiredExactVersion, MismatchedClusterCountByDesiredExactVersion, FailedClusterCountByDesiredExactVersion, ClusterCountByAchievedExactVersion, SuccessfulClusterCountByAchievedExactVersion}`.
- **Sync** (pure fn `computeRolloutStatusCounts` over the list of SPCs in the channel's minor):
  - *Desired*: count by `Spec…DesiredVersion`.
  - *Achieved*: `Spec…DesiredVersion == earliest(active_versions)` (design: "earliest version in activeVersions"; the active list is newest-first so "earliest" = the completed base — see §8).
  - *Mismatched*: desired set but not achieved.
  - *Failed*: mismatched for longer than `maxUpgradeDuration[minor]` (requires an observation timestamp — see §8).
  - *Successful*: achieved and stable for longer than `minVersionReadyDuration`.

### 5.4 Control Plane Version Best Version Selection (per-rollout, interval)
- **Inputs**: `zStreamOffset`, Cincinnati best version for the channel, `minimumVersions[channel]`.
- **Output**: `rollout.Spec.BestExactVersion`.
- **Sync** (pure fn `selectBestExactVersion`):
  - Compute the graph best (most recent risk-free z-stream, offset by `zStreamOffset`) via `internal/cincinnati` (reuse `FindBestVersionInMinor`-style logic, lifted to channel scope).
  - `bestExactVersion = max(minimumVersions[channel], graphBest)`.

### 5.5 Normal Cluster Desired Version Assignment (per-rollout, interval)
- **Inputs**: `rollout.Spec.BestExactVersion`, `rollout.Status.*`, per-SPC `desired_version`/`active_versions`/`pinnedVersion`.
- **Output**: `SPC.Spec…DesiredVersion` for a bounded set of clusters.
- **Sync** (pure fns `eligibleClusters`, `rolloutDecision`):
  1. Failure budget: if `FailedClusterCount[best] > 2` **or** `> 5%` of clusters at `best` → failure condition, return.
  2. `EligibleClusters` = clusters in the channel's minor with `desired < best`, and either no pin, or pinned with `untilExactVersion <= best`.
  3. If no eligible → stable condition, return.
  4. Canary: if `(Mismatched+Achieved)[best] < canaryPercentage%+2` → pick N (random for now) eligible, set desired=best, return.
  5. Gate: if `Successful[best] < canaryPercentage%` → progressing condition, return.
  6. Rolling: if `(Mismatched+Achieved)[best] < rollingPercentage%` → pick N eligible, set desired=best, return.
- Respect the "≥60s since last rollout" NeedsWork guard so caches settle (a cooldown on the syncer).

## 6. Coexistence & cutover with `ControlPlaneDesiredVersion`

`ControlPlaneDesiredVersion` and the new assignment controllers both write
`SPC.Spec…DesiredVersion`; running both would fight. Options (recommend **B**):

- **A. Hard replace** — delete `ControlPlaneDesiredVersion`, rely entirely on the
  rollout controllers. Simplest but riskiest; requires rollouts to exist for
  every active channel before cutover.
- **B. Feature-flagged handoff** — a backend flag selects which owns
  `DesiredVersion`. Ship the rollout controllers dark, enable per-region. The
  existing controller's Cincinnati logic is reused by 5.4, so this is mostly
  moving the write site.
- **C. Layered** — `ControlPlaneDesiredVersion` keeps handling initial install /
  y-stream (customer-chosen minor); rollout controllers own z-stream progression
  within a minor. More moving parts.

## 7. Testing strategy

- **Pure decision functions** (`computeForcedDesiredVersion`,
  `computeRolloutStatusCounts`, `selectBestExactVersion`, `eligibleClusters`,
  `rolloutDecision`) get exhaustive table-driven unit tests — no fakes needed.
- **`SyncOnce`** tests use the in-memory mock DB
  (`corecosmosstoragetesting.NewMockResourcesDBClient`) + slice-backed fake
  listers (`corelistertesting.Slice*Lister`, new `fleetlistertesting` fakes),
  asserting the Cosmos side-effect by reading back through the mock, and covering
  the `IsPreconditionFailedError` no-op path. Randomised canary/rolling selection
  is made deterministic in tests via an injectable selector (interface, not a
  closure).
- No `FRONTEND_SIMULATION_TESTING`; unit tests use fakes only.

## 8. Open questions (flagged for the author)

1. **5.2 step 2** and **5.5** both say to set desired to `bestExactVersion` even
   in the "still pinned" branch — but the pin's purpose is to hold at
   `exactVersion`. This implementation pins to `exactVersion` in the still-pinned
   branch and treats the doc as a typo; confirm.
2. **"earliest version in activeVersions"** — `ActiveVersions` is documented
   newest-first, so "earliest" = last element (the completed base). Confirm
   whether "achieved" means the newest completed equals `best`, or the base does.
3. **Failed/Successful counts** need a per-cluster timestamp of when the
   desired/achieved transition happened (for `maxUpgradeDuration` /
   `minVersionReadyDuration`). Proposed: derive from existing SPC status
   conditions / `HCPClusterActiveVersion`, else add a `LastTransitionTime`.
4. **Rollout object lifecycle** — who creates a `ControlPlaneVersionRollout` per
   active channel (a discovery controller vs. on-demand from
   `DesiredVersionChannels`)?

## 9. Implementation status

Implemented (compiles + unit-tested + linted):
- New API types (`ControlPlaneVersionRollout`, SPC `PinnedVersion`) + deepcopy.
- `RolloutConfig`.
- The four new controllers (5.2–5.5) as syncers with factored pure logic, depending on lister/DB interfaces.
- Exhaustive unit tests for the pure logic and `SyncOnce` happy/precondition paths.
- **Full fleet Cosmos wiring** for `ControlPlaneVersionRollout`: `fleetcosmosstorage`
  CRUD + global lister, `FleetPartitionKeyDeriver`, validation, and the
  `fleetcosmosstoragetesting` mock (CRUD, seeding, global lister).
- **Fleet informer + lister**: `fleetinformers.ControlPlaneVersionRollouts()` and
  `fleetlisters.ControlPlaneVersionRolloutLister` (+ slice test fake).
- **Fleet watching controller**: `controllerutils.NewControlPlaneVersionRolloutWatchingController`
  (keyed by `ControlPlaneVersionRolloutKey`), and per-controller
  `New…Controller` constructors wrapping the syncers.
- **Backend registration under leader election** (`backend/pkg/app/backend.go`),
  gated by the `--enable-version-rollout` flag.
- **Backend flags/config** (`backend/cmd/root.go`): `--enable-version-rollout`,
  `--version-rollout-zstream-offset`, `--version-rollout-canary-percentage`,
  `--version-rollout-rolling-percentage`, `--version-rollout-min-version-ready-duration`,
  threaded into `app.BackendOptions.VersionRolloutConfig`.
- **Real Cincinnati `BestVersionSelector`** (`NewCincinnatiBestVersionSelector`)
  reusing the exported `version.FindAllUpgradeTargetVersionsInMinor` enumeration
  plus a pure `selectVersionWithOffset` for the z-stream offset.
- **Version-transition ages** via an in-process `inMemoryVersionAgeSource` (feeds
  the Failed/Successful counts without persisting a timestamp on the API — see
  the caveat below).
- **Coexistence cutover** (§6 option B): when `--enable-version-rollout` is set,
  the backend runs the rollout controllers and does **not** run the per-cluster
  `ControlPlaneDesiredVersion` controller, so they never both write
  `SPC.Spec…DesiredVersion`.

Remaining follow-ups (design-gated or ops):
- `config.yaml`/schema plumbing to set the flags per environment, and populating
  `RolloutConfig.MinimumVersions` / `MaxUpgradeDuration` (currently default-empty:
  no SRE floor and no upgrade-failure timeout until configured).
- A **persisted** per-cluster version-transition timestamp to replace the
  in-memory age source (survives restarts; enables accurate `MaxUpgradeDuration`
  failure detection). The in-memory source only makes detection more
  conservative on restart, never wrong.
- Admission/data-flow-doc updates and the open questions in §8.
