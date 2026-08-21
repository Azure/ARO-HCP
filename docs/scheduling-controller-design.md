# HCP Scheduling Controller Design

## Overview

This design moves HCP placement decisions from Cluster Service into the ARO HCP backend. A new PlacementController selects a management cluster for each HCP, pins the CS provision shard, and gates the creation pipeline on placement. The work is split into three independently mergeable phases.

## Phases

1. **[Simple Placement + Provision Shard Pinning](scheduling-phase1-simple-placement.md)** — PlacementController picks an eligible MC by SPC count (bin-packing), writes `ServiceProviderCluster.Spec.ManagementClusterResourceID`, pins provision shard on CS cluster creation. No scheduling doc changes.

2. **[Capacity-Aware Scheduling](scheduling-phase2-capacity-aware.md)** — PlacementController uses `ObservedResources`, `ScaleCeiling`, and `HCPResourceRequirements` for capacity-aware two-pass fit. Adds `PendingAssignedClusters` to the scheduling doc. Introduces ManagementClusterAssignmentSync for pending cleanup.

3. **[Observation-Based Pending Cleanup](scheduling-phase3-observation-based-cleanup.md)** — Mirrors `ReadyResourceIDs`/`NotReadyResourceIDs` from CapacityReport to the scheduling doc. ManagementClusterAssignmentSync removes pending entries when the HCP is observed, closing the capacity reservation lifecycle.

## Key Design Decisions

- **Backend owns scheduling.** Backend has access to both core and fleet Cosmos containers and runs per-region.
- **Single worker.** Eliminates concurrent scheduling decisions within one backend instance.
- **Spec/Status split.** `ServiceProviderCluster.Spec.ManagementClusterResourceID` is scheduler intent; Status is confirmed reality from CS. `ManagementClusterPlacementSync` keeps Status honest during transition.
- **Creation pipeline gating.** `ClusterPendingClusterServiceIDAssign` gates on Spec — scheduler becomes the creation pipeline entrypoint.
- **Provision shard pinning.** CS accepts `provision_shard_id` in the cluster properties map, bypassing its own round-robin selection. `ClusterClusterServiceCreate` passes the MC's `ClusterServiceProvisionShardID` as a `requiredProperty` to `BuildCSCluster`.
