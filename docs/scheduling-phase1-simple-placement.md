# Phase 1: Simple Placement + Provision Shard Pinning

## Goal

Move HCP placement decisions from Cluster Service into the backend with a minimal scheduling strategy. Validate end-to-end that CS honors the pinned provision shard.

No scheduling doc changes. No pending lists. No capacity math.

## Changes

### New field: `ServiceProviderCluster.Spec.ManagementClusterResourceID`

Add `ManagementClusterResourceID *azcorearm.ResourceID` to `ServiceProviderClusterSpec`. The PlacementController writes Spec (intent), `ManagementClusterPlacementSync` continues writing Status (confirmed reality from CS).

### New controller: PlacementController (cluster-keyed, single worker)

- **Trigger:** `ServiceProviderCluster.Spec.ManagementClusterResourceID` is nil
- **Reads:**
  - Lister: `ServiceProviderCluster`, `ManagementCluster`
- **Logic:**
  - Filter eligible MCs: `ManagementCluster.Spec.SchedulingPolicy == Schedulable` AND `ManagementCluster.Status.Conditions[Ready] == True`
  - Count SPCs per eligible MC (lister scan of all SPCs, count by `ServiceProviderCluster.Spec.ManagementClusterResourceID`)
  - Tie-break: pick MC with highest SPC count (bin-packing). This is intentional for initial testing — bin-packing produces a visibly different distribution than CS's own round-robin selection, making it observable whether CS honors the pinned provision shard during E2E tests.
  - Pure function: all inputs in, MC resource ID out.
- **Writes:**
  - `ServiceProviderCluster.Spec.ManagementClusterResourceID`: set to MC resource ID — if conflict, retry
- **Interruptibility:** single write, no partial failure scenarios.

### Modified: ClusterPendingClusterServiceIDAssign

- Add `serviceProviderClusterLister` dependency
- New precondition in `needsWork`: `ServiceProviderCluster.Spec.ManagementClusterResourceID != nil`
- No other changes

### Modified: ClusterClusterServiceCreate

- Read `ServiceProviderCluster.Spec.ManagementClusterResourceID` to look up `ManagementCluster.Status.ClusterServiceProvisionShardID`
- Pass provision shard as `requiredProperty` to `BuildCSCluster`:

```go
provisionShardID := managementCluster.Status.ClusterServiceProvisionShardID.ID()
requiredProperties := map[string]string{
    "provision_shard_id": provisionShardID,
}
BuildCSCluster(cluster.ID, tenantID, cluster, requiredProperties, nil, serviceProviderCluster)
```

### Kept: ManagementClusterPlacementSync

- Continues writing `ServiceProviderCluster.Status.ManagementClusterResourceID` from CS provision shard data
- Handles transition: clusters created before this change still get Status populated from CS
- Post-transition: confirms Status matches Spec

## What this phase does NOT include

- No `ManagementClusterScheduling.Status.PendingAssignedClusters` (phase 2)
- No capacity-aware scheduling (phase 2)
- No `HCPResourceRequirements` usage (phase 2)
- No two-pass fit algorithm (phase 2)
- No `ManagementClusterAssignmentSync` controller (phase 2)
- No `ReadyResourceIDs`/`NotReadyResourceIDs` mirroring (phase 3)

## Testing

- Unit tests for PlacementController (tabular: eligible MCs, SPC counts, expected pick)
- Unit tests for modified `needsWork` in ClusterPendingClusterServiceIDAssign
- Unit tests for provision shard pinning in ClusterClusterServiceCreate
- E2E: create multiple clusters, verify they bin-pack onto one MC (not round-robin across MCs)
