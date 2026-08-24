# Phase 1: Swift-NIC Scheduling + Provision Shard Pinning

## Goal

Move HCP placement decisions from Cluster Service into the backend with a swift-NIC-based scheduling strategy. Each HCP consumes exactly 3 swift-NICs. The PlacementController selects the eligible management cluster with the highest available capacity (spread — distribute load evenly rather than concentrating it). Ties are broken deterministically by lowest resource ID.

This approximation ignores that 2024 API version clusters consume fewer than 3 swift-NICs. Accepted tradeoff — overly conservative, never overbooks.

## Prerequisites

- Phase 0 merged (ReadyResourceIDs/NotReadyResourceIDs on scheduling doc)

## Capacity Formula

```
available = ScaleCeiling.Capacity["aro.openshift.io/swift-nic"]
          - ObservedResources.Usage["aro.openshift.io/swift-nic"]
          - countNonEmpty(NotReadyResourceIDs) * 3
          - countNonNil(PendingAssignedClusters) * 3

fit if available >= 3
```

- **ReadyResourceIDs** — already counted in `ObservedResources.Usage`. No reservation needed.
- **NotReadyResourceIDs** — exist on the MC but might not be reflected in usage yet. Reserve 3 NICs each. Only non-empty entries are counted (nil/empty entries do not correspond to a real HCP).
- **PendingAssignedClusters** — scheduled but not yet observed on MC. Reserve 3 NICs each. Only non-nil entries are counted.
- **ScaleCeiling.Capacity** — maximum possible capacity. Correct bound when reserving against worst-case consumption (not-ready + pending might all materialize).

## Changes

### New field: `ServiceProviderCluster.Spec.ManagementClusterResourceID`

Add `ManagementClusterResourceID *azcorearm.ResourceID` to `ServiceProviderClusterSpec`. The PlacementController writes Spec (scheduler intent). `ManagementClusterPlacementSync` continues writing Status (CS-confirmed reality). Mismatch between Spec and Status means CS ignored our provision shard pinning — ManagementClusterPlacementSync logs the drift but does NOT mutate Spec (Spec is owned by PlacementController). Drift is surfaced for investigation, not auto-corrected.

### New field: `ManagementClusterScheduling.Status.PendingAssignedClusters`

Add `PendingAssignedClusters []*azcorearm.ResourceID` to `ManagementClusterSchedulingStatus`. Tracks HCPs that have been scheduled to this MC but are not yet observed (not in `ReadyResourceIDs ∪ NotReadyResourceIDs`). Transient capacity reservation — drains as HCPs appear in ready/notReady.

### New controller: PlacementController (cluster-keyed, single worker)

- **Trigger:** `ServiceProviderCluster.Spec.ManagementClusterResourceID` is nil AND `ServiceProviderCluster.Status.ManagementClusterResourceID` is nil
- **Reads:**
  - Lister: `ServiceProviderCluster`, `ManagementCluster`
  - Live: all eligible MCs' `ManagementClusterScheduling` docs (for capacity data + ReadyResourceIDs + NotReadyResourceIDs + PendingAssignedClusters)
- **Logic:**
  - Filter eligible MCs: `ManagementCluster.Spec.SchedulingPolicy == Schedulable` AND `ManagementCluster.Status.Conditions[Ready] == True`
  - For each eligible MC, compute available swift-NICs using the capacity formula above
  - Reject MCs with `available < 3`
  - Tie-break: pick MC with highest available (spread — distribute load evenly across MCs). Equal capacity → lowest resource ID wins (deterministic, stable).
  - Pure function: all inputs in, MC resource ID out.
- **Rollout/transition handling:** when an SPC has `Spec == nil` but `Status.ManagementClusterResourceID` is already set (previously placed by `ManagementClusterPlacementSync`), backfill `Spec = Status` rather than re-scheduling.
- **Writes:**
  1. `ManagementClusterScheduling.Status.PendingAssignedClusters`: add HCP resource ID — if conflict, retry
  2. `ServiceProviderCluster.Spec.ManagementClusterResourceID`: set to MC resource ID — if conflict, retry
- **Interruptibility:**
  - Crash after step 1, before step 2: HCP in PendingAssignedClusters, SPC Spec unset. On restart, PlacementController re-runs. May pick same or different MC. PendingCleanupController handles stale entries if different MC picked. SPC Spec nil means placement still in progress — pending entry is preserved.

### Pending cleanup: CapacityReportingController (observation-based)

CapacityReportingController already writes `ReadyResourceIDs`/`NotReadyResourceIDs` to the scheduling doc. In the same write, it removes `PendingAssignedClusters` entries that appear in `ReadyResourceIDs ∪ NotReadyResourceIDs`. Atomic — observation and cleanup in one operation.

### Pending cleanup: PendingCleanupController (stale-entry sweep)

Periodic, MC-keyed controller (10-minute resync). For each entry in `ManagementClusterScheduling.Status.PendingAssignedClusters`, the effective placement is determined as `Status.ManagementClusterResourceID` when set (CS reality takes precedence), otherwise `Spec.ManagementClusterResourceID`:

| SPC state | Action |
|---|---|
| Effective placement points at this MC | **Keep** — valid pending |
| Effective placement points at different MC | **Remove** — stale, cluster re-scheduled elsewhere |
| Effective placement is nil (both Spec and Status nil) | **Keep** — placement still in progress (crash between pending write and SPC write) |
| SPC does not exist | **Remove** — cluster deleted |

Uses `ServiceProviderClusterLister` (cache, not direct DB gets). Cross-container read (fleet → core) is acceptable for a periodic sweep.

### Modified: ManagementClusterPlacementSync

- Resolves `ServiceProviderCluster.Status.ManagementClusterResourceID` from CS provision shard data **only when Status is not yet set**. Once a provision shard has been observed and recorded, the CS lookup is skipped on subsequent syncs.
- If `Spec.ManagementClusterResourceID` is set AND `Status.ManagementClusterResourceID` is set AND `Spec != Status`:
  - **Log error** with both values (scheduler-intended MC vs CS-actual MC) — signal that provision shard pinning is broken
  - **Does NOT mutate Spec** — Spec is owned by PlacementController. Drift is surfaced via logs and the `backend_cluster_placement_state{state="mismatch"}` metric for investigation, not auto-corrected.
- Handles transition: clusters created before this change still get Status populated from CS

### Modified: ClusterPendingClusterServiceIDAssign

- Add `serviceProviderClusterLister` dependency
- New precondition in `needsWork`: `ServiceProviderCluster.Spec.ManagementClusterResourceID != nil`
- No other changes

### Modified: ClusterClusterServiceCreate

- Read `ServiceProviderCluster.Spec.ManagementClusterResourceID` to look up `ManagementCluster.Status.ClusterServiceProvisionShardID`
- Pin provision shard via OCM SDK builder method:

```go
provisionShardID := managementCluster.Status.ClusterServiceProvisionShardID.ID()
csClusterBuilder.ProvisionShardID(provisionShardID)
```

Uses `ClusterBuilder.ProvisionShardID(string)` — not a cluster property map. The SDK method sets the provision shard directly on the CS cluster object. Implementation: resolve MC from `Spec.ManagementClusterResourceID` → extract stamp identifier from parent resource ID (`managementClusterResourceID.Parent.Name`) → lister `.Get(stampIdentifier)` → read `ClusterServiceProvisionShardID`.

## Observability

### Metrics

- **`backend_cluster_info`** — Existing info-style gauge extended with placement state labels. Exposes placement status (`placed`, `unplaced`, `mismatch`) per cluster rather than a separate aggregate gauge. `mismatch` = Spec set AND Status set AND Spec != Status.
- **`backend_cluster_placement_delay_seconds`** — Histogram measuring creation→placement latency (`time.Since(cluster.SystemData.CreatedAt)`), observed once per fresh placement (not on rollout backfill). Buckets: 1, 5, 10, 30, 60, 120, 300, 600, 1800 seconds.

### Alert

- **`SchedulerPlacementMismatch`** — Warning alert firing when `backend_cluster_placement_state{state="mismatch"} > 0` for 15 minutes. Defined in `observability/alerts/scheduler-placement-prometheusRule.yaml` with a promtool test. Registered in `alerts-rp-services.yaml`.

### CI Visualization

Two panels added to `test/cmd/aro-hcp-tests/gather-observability/queries.yaml`:
- Placement-delay percentile chart (p50/p90/p99 over `backend_cluster_placement_delay_seconds_bucket`)
- Stacked placement-state chart by `state`

## What this phase does NOT include

- No multi-resource capacity math (phase 2)
- No `HCPResourceRequirements` usage (phase 2)
- No two-pass fit algorithm / current-capacity pass (phase 2)

## Testing

- Unit tests for PlacementController (tabular: eligible MCs, capacity/usage/notReady/pending combos, expected pick with spread strategy)
- Unit tests for capacity formula edge cases (zero capacity, full ceiling, notReady eating all slack, nil pending entries)
- Unit tests for CapacityReportingController pending cleanup (observed HCP removed from pending)
- Unit tests for PendingCleanupController (stale entry removal: effective MC points elsewhere → remove, effective MC nil → keep, SPC deleted → remove)
- Unit tests for rollout backfill (Spec nil + Status set → backfill, not re-schedule)
- Unit tests for ManagementClusterPlacementSync drift detection (Spec != Status → log-only, no Spec mutation)
- Unit tests for modified `needsWork` in ClusterPendingClusterServiceIDAssign (gates on SPC.Spec.ManagementClusterResourceID)
- Unit tests for provision shard pinning in ClusterClusterServiceCreate
- Unit tests for placement metrics (placement state on `backend_cluster_info`, placement delay histogram)
