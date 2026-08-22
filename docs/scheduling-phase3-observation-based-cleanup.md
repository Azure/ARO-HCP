# Phase 3: Observation-Based Pending Cleanup

## Goal

Mirror CapacityReport resource ID lists to the scheduling document so ManagementClusterAssignmentSync can remove pending entries when the HCP is observed on the management cluster. This closes the capacity reservation lifecycle: pending entries are removed when `ObservedResources` covers the HCP.

## Prerequisites

- Phase 2 merged (capacity-aware scheduling, pending list, ManagementClusterAssignmentSync)

## Changes

### New fields on `ManagementClusterScheduling.Status`

- `ReadyResourceIDs []string` — ARM resource IDs of HCPs whose HostedCluster is ready
- `NotReadyResourceIDs []string` — ARM resource IDs of HCPs whose HostedCluster exists but is not ready

Mirrored from `CapacityReport.Status.HostedControlPlanes.ReadyResourceIDs` and `CapacityReport.Status.HostedControlPlanes.NotReadyResourceIDs`.

### Modified: CapacityReportingController (fleet)

- Mirrors `ReadyResourceIDs` and `NotReadyResourceIDs` from the CapacityReport CR to `ManagementClusterScheduling.Status`
- Same write path as existing capacity/usage/requests mirroring

### Enhanced: ManagementClusterAssignmentSync

- **New read:** `ManagementClusterScheduling.Status.ReadyResourceIDs`
- **New logic:** for each entry in `ManagementClusterScheduling.Status.PendingAssignedClusters`:
  - HCP in `ManagementClusterScheduling.Status.ReadyResourceIDs` → remove from pending (observed, capacity covered by `ManagementClusterScheduling.Status.ObservedResources`)
  - SPC-based cleanup unchanged from phase 2

This completes the pending lifecycle: HCP enters pending at scheduling time, leaves pending when its resource consumption is reflected in `ObservedResources` (signaled by appearing in `ReadyResourceIDs`).

## Testing

- Unit tests for CapacityReportingController resource ID mirroring
- Unit tests for ManagementClusterAssignmentSync observation-based cleanup
- Integration test: schedule HCP → verify pending → CapacityReport picks up HCP → verify pending cleared
