# Phase 2: Capacity-Aware Scheduling

## Goal

Replace the simple SPC-count bin-packing from phase 1 with capacity-aware scheduling using existing fleet scheduling data. Add pending list for capacity reservation during the placement-to-observation gap.

## Prerequisites

- Phase 1 merged (PlacementController, provision shard pinning, SPC.Spec.ManagementClusterResourceID)

## Changes

### New field: `ManagementClusterScheduling.Status.PendingAssignedClusters`

Add `PendingAssignedClusters []*azcorearm.ResourceID` to `ManagementClusterSchedulingStatus`. Tracks HCPs that have been scheduled to this MC but are not yet observed in `ObservedResources`.

### New controller: ManagementClusterAssignmentSync (MC-keyed, multi-worker OK)

- **Trigger:** periodic resync + `ManagementClusterScheduling` informer events
- **Reads:**
  - Lister: `ServiceProviderCluster` (check `ServiceProviderCluster.Spec.ManagementClusterResourceID`)
  - `ManagementClusterScheduling.Status.PendingAssignedClusters`
- **Logic:** for each entry in `ManagementClusterScheduling.Status.PendingAssignedClusters`:
  - `ServiceProviderCluster.Spec.ManagementClusterResourceID` points to this MC → keep (valid pending)
  - `ServiceProviderCluster.Spec.ManagementClusterResourceID` points to different MC → remove (stale, double-pending from crash recovery)
  - `ServiceProviderCluster` doesn't exist → remove (cluster deleted)
- **Writes:**
  - `ManagementClusterScheduling.Status.PendingAssignedClusters`: updated list — if conflict, retry
- **Note:** phase 3 adds `ReadyResourceIDs`-based cleanup (remove pending when HCP is observed). Until then, pending entries for valid SPCs remain until the SPC is deleted or reassigned.

### Enhanced: PlacementController

- **New reads:**
  - Lister: `HCPResourceRequirements`
  - Live: all MCs' `ManagementClusterScheduling` docs (replaces lister-based SPC counting)
- **New logic:**
  - Compute consumed per MC: `max(ManagementClusterScheduling.Status.ObservedResources.Requests, ManagementClusterScheduling.Status.ObservedResources.Usage)`
  - Compute available capacity per MC: `capacity - consumed - (len(ManagementClusterScheduling.Status.PendingAssignedClusters) * perHCPReservation)`
  - `perHCPReservation` = `max(HCPResourceRequirements.Status.AverageUsage, HCPResourceRequirements.Status.AverageRequests)` per resource dimension
  - Pass 1: fit into `ManagementClusterScheduling.Status.ObservedResources.Capacity`
  - Fallback pass 2: fit into `ManagementClusterScheduling.Status.ScaleCeiling.Capacity`
  - Tie-break: pick most loaded that still fits (bin-packing, same rationale as phase 1)
- **New writes:**
  - a. `ManagementClusterScheduling.Status.PendingAssignedClusters`: add HCP resource ID — if conflict, retry
  - b. `ServiceProviderCluster.Spec.ManagementClusterResourceID`: set to MC resource ID — if conflict, retry (unchanged from phase 1)
- **Interruptibility:**
  - Crash after a, before b: HCP in `PendingAssignedClusters`, SPC unset. On restart, PlacementController re-runs. May pick same or different MC. ManagementClusterAssignmentSync cleans stale pending if different MC picked.
- **Stale lister mitigation:** live-reads `ManagementClusterScheduling` docs from Cosmos instead of listers. Each scheduling decision sees its own prior writes.

## What this phase does NOT include

- No `ReadyResourceIDs`/`NotReadyResourceIDs` mirroring (phase 3)
- No observation-based pending cleanup — ManagementClusterAssignmentSync only checks SPC state, not CapacityReport (phase 3)

## Testing

- Unit tests for capacity formula (tabular: various capacity/usage/pending combinations)
- Unit tests for two-pass fit (pass 1 insufficient → falls back to pass 2)
- Unit tests for ManagementClusterAssignmentSync (stale pending cleanup cases)
- Unit tests for tie-breaking (bin-packing: picks most loaded that fits)
- Integration tests with mock scheduling docs and HCPResourceRequirements
