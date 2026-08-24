# Phase 1: Swift-NIC Scheduling + Provision Shard Pinning

## Goal

Move HCP placement decisions from Cluster Service into the backend with a swift-NIC-based scheduling strategy. Each HCP consumes exactly 3 swift-NICs. The PlacementController selects the most loaded management cluster that still has capacity (bin-packing by swift-NIC availability against the scale ceiling).

This approximation ignores that 2024 API version clusters consume fewer than 3 swift-NICs. Accepted tradeoff — overly conservative, never overbooks.

## Prerequisites

- Phase 0 merged (ReadyResourceIDs/NotReadyResourceIDs on scheduling doc)

## Capacity Formula

```
available = ScaleCeiling.Capacity["swift-nic"]
          - ObservedResources.Usage["swift-nic"]
          - (len(NotReadyResourceIDs) * 3)
          - (len(PendingAssignedClusters) * 3)

fit if available >= 3
```

- **ReadyResourceIDs** — already counted in `ObservedResources.Usage`. No reservation needed.
- **NotReadyResourceIDs** — exist on the MC but might not be reflected in usage yet. Reserve 3 NICs each.
- **PendingAssignedClusters** — scheduled but not yet observed on MC. Reserve 3 NICs each.
- **ScaleCeiling.Capacity** — maximum possible capacity. Correct bound when reserving against worst-case consumption (not-ready + pending might all materialize).

## Changes

### New field: `ServiceProviderCluster.Spec.ManagementClusterResourceID`

Add `ManagementClusterResourceID *azcorearm.ResourceID` to `ServiceProviderClusterSpec`. The PlacementController writes Spec (scheduler intent). `ManagementClusterPlacementSync` continues writing Status (CS-confirmed reality). Mismatch between Spec and Status is a hard error — CS ignored our provision shard pinning.

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
  - Tie-break: pick MC with lowest available (bin-packing — fill up MCs before moving to next)
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

Periodic, MC-keyed controller. For each entry in `ManagementClusterScheduling.Status.PendingAssignedClusters`:

| SPC state | Action |
|---|---|
| `Spec.ManagementClusterResourceID` points at this MC | **Keep** — valid pending |
| `Spec.ManagementClusterResourceID` points at different MC | **Remove** — stale, cluster re-scheduled elsewhere |
| `Spec.ManagementClusterResourceID` is nil | **Keep** — placement still in progress (crash between pending write and SPC write) |
| SPC does not exist | **Remove** — cluster deleted |

Cross-container read (fleet → core) is acceptable for a periodic sweep.

### Modified: ManagementClusterPlacementSync

- **Always** resolves `ServiceProviderCluster.Status.ManagementClusterResourceID` from CS provision shard data (does not skip when Status is already set)
- If `Spec.ManagementClusterResourceID` is set AND `Status.ManagementClusterResourceID` is set AND `Spec != Status`:
  - **Log error** with both values (scheduler-intended MC vs CS-actual MC) — signal that provision shard pinning is broken
  - **Overwrite** `Spec.ManagementClusterResourceID = Status.ManagementClusterResourceID` — accept CS's reality so the system self-heals (PendingCleanupController will clear the stale pending entry on the originally intended MC)
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

Uses `ClusterBuilder.ProvisionShardID(string)` — not a cluster property map. The SDK method sets the provision shard directly on the CS cluster object. See PR #6651 for the implementation pattern: resolve MC from `Spec.ManagementClusterResourceID` → extract stamp identifier from parent resource ID → lister `.Get(stampIdentifier)` → read `ClusterServiceProvisionShardID`.

## What this phase does NOT include

- No multi-resource capacity math (phase 2)
- No `HCPResourceRequirements` usage (phase 2)
- No two-pass fit algorithm / current-capacity pass (phase 2)

## Testing

- Unit tests for PlacementController (tabular: eligible MCs, capacity/usage/notReady/pending combos, expected pick)
- Unit tests for capacity formula edge cases (zero capacity, full ceiling, notReady eating all slack)
- Unit tests for CapacityReportingController pending cleanup (observed HCP removed from pending)
- Unit tests for PendingCleanupController (stale entry removal: SPC points elsewhere → remove, SPC nil → keep, SPC deleted → remove)
- Unit tests for rollout backfill (Spec nil + Status set → backfill, not re-schedule)
- Unit tests for ManagementClusterPlacementSync mismatch detection (Spec != Status → error)
- Unit tests for modified `needsWork` in ClusterPendingClusterServiceIDAssign
- Unit tests for provision shard pinning in ClusterClusterServiceCreate
- E2E: create multiple clusters, verify they bin-pack onto most loaded MC
