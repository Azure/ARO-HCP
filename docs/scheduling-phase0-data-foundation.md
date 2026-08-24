# Phase 0: Scheduling Data Foundation

## Goal

Mirror `ReadyResourceIDs` and `NotReadyResourceIDs` from CapacityReport to `ManagementClusterScheduling.Status`. This is pure data plumbing — no scheduling logic changes, no new controllers. The data soaks in production before any consumer depends on it.

## Changes

### New fields on `ManagementClusterScheduling.Status`

- `ReadyResourceIDs []string` — ARM resource IDs of HCPs whose HostedCluster is ready
- `NotReadyResourceIDs []string` — ARM resource IDs of HCPs whose HostedCluster exists but is not ready

Mirrored from `CapacityReport.Status.HostedControlPlanes.ReadyResourceIDs` and `CapacityReport.Status.HostedControlPlanes.NotReadyResourceIDs`.

### Modified: CapacityReportingController (fleet)

- Mirrors `ReadyResourceIDs` and `NotReadyResourceIDs` from the CapacityReport CR to `ManagementClusterScheduling.Status`
- Same write path as existing capacity/usage/requests mirroring

## What this phase does NOT include

- No PlacementController (phase 1)
- No scheduling logic changes (phase 1)
- No `PendingAssignedClusters` (phase 1)

## Testing

- Unit tests for CapacityReportingController resource ID mirroring
- Verify `ManagementClusterScheduling.Status.ReadyResourceIDs` / `NotReadyResourceIDs` match CapacityReport source data
