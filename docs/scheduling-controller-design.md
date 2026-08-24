# HCP Scheduling Controller Design

## Overview

This design moves HCP placement decisions from Cluster Service into the ARO HCP backend. A new PlacementController selects a management cluster for each HCP, pins the CS provision shard, and gates the creation pipeline on placement. The work is split into three independently mergeable phases.

## Phases

0. **[Scheduling Data Foundation](scheduling-phase0-data-foundation.md)** — Mirror `ReadyResourceIDs`/`NotReadyResourceIDs` from CapacityReport to `ManagementClusterScheduling.Status`. Pure data plumbing, no scheduling logic changes.

1. **[Swift-NIC Scheduling + Provision Shard Pinning](scheduling-phase1-swift-nic-scheduling.md)** — PlacementController picks an eligible MC by swift-NIC capacity against scale ceiling (bin-packing). Adds `PendingAssignedClusters` to the scheduling doc with cleanup via `ReadyResourceIDs`/`NotReadyResourceIDs`. Pins provision shard on CS cluster creation. ManagementClusterPlacementSync detects CS pinning violations.

2. **[Resource-Based Scheduling](scheduling-phase2-resource-based-scheduling.md)** — PlacementController uses multi-resource capacity-aware fit with `HCPResourceRequirements`. Free to use current capacity, scale ceiling, or any elaboration.

## Key Design Decisions

- **Backend owns scheduling.** Backend has access to both core and fleet Cosmos containers and runs per-region.
- **Single worker.** Eliminates concurrent scheduling decisions within one backend instance.
- **Spec/Status split on SPC.** `ServiceProviderCluster.Spec.ManagementClusterResourceID` is scheduler intent (PlacementController writes); `ServiceProviderCluster.Status.ManagementClusterResourceID` is confirmed reality from CS (ManagementClusterPlacementSync writes). Mismatch between Spec and Status means CS ignored our pinning — ManagementClusterPlacementSync logs error and overwrites Spec with Status (accept CS reality, self-heal).
- **Scheduling doc state.** `ManagementClusterScheduling.Status.PendingAssignedClusters` tracks transient capacity reservations (PlacementController writes). `ManagementClusterScheduling.Status.ReadyResourceIDs`/`NotReadyResourceIDs` are observed reality from CapacityReport. No explicit assigned-clusters field — `ReadyResourceIDs ∪ NotReadyResourceIDs` is the assigned set.
- **Two-path pending cleanup.** CapacityReportingController removes pending entries when HCPs appear in ready ∪ notReady (observation-based, same write). PendingCleanupController removes stale entries where SPC.Spec points to a different MC or the SPC no longer exists (periodic sweep). SPC.Spec nil means placement in progress — pending entry preserved.
- **Creation pipeline gating.** `ClusterPendingClusterServiceIDAssign` gates on `SPC.Spec.ManagementClusterResourceID` — scheduler becomes the creation pipeline entrypoint.
- **Provision shard pinning.** `ClusterClusterServiceCreate` resolves the placed MC's `ClusterServiceProvisionShardID` and pins it via `ClusterBuilder.ProvisionShardID(string)` (OCM SDK method, not a cluster property).
