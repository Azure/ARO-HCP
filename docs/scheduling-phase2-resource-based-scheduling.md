# Phase 2: Resource-Based Scheduling

## Goal

Replace the swift-NIC-only scheduling from phase 1 with multi-resource capacity-aware scheduling using `ObservedResources`, `ScaleCeiling`, and `HCPResourceRequirements`. The scheduling strategy can use both current observed capacity and the scale ceiling, and can employ any elaboration (e.g. two-pass, weighted scoring, priority tiers).

## Prerequisites

- Phase 1 merged (PlacementController, provision shard pinning, PendingAssignedClusters, pending cleanup, mismatch detection)

## Changes

### Enhanced: PlacementController

- **New reads:**
  - Lister: `HCPResourceRequirements`
- **New logic:**
  - Multi-resource fit across cpu, memory, swift-nic (and any future scheduling-relevant resources)
  - Per-HCP reservation from `HCPResourceRequirements` (replaces fixed `3 * swift-nic`)
  - Consumed per MC: `max(ObservedResources.Requests, ObservedResources.Usage)` per resource dimension
  - Available capacity per MC: `capacity - consumed - (len(NotReadyResourceIDs) * perHCPReservation) - (len(PendingAssignedClusters) * perHCPReservation)`
  - `perHCPReservation` = `max(HCPResourceRequirements.Status.AverageUsage, HCPResourceRequirements.Status.AverageRequests)` per resource dimension
  - Free to use current capacity, scale ceiling, or both (e.g. two-pass: fit into current first, fall back to ceiling)
  - Tie-break: bin-packing (most loaded that fits)
- **Stale lister mitigation:** live-reads `ManagementClusterScheduling` docs from Cosmos instead of listers. Each scheduling decision sees its own prior writes.

### Pending cleanup

Unchanged from phase 1. Same two rules:
- HCP in `ReadyResourceIDs ∪ NotReadyResourceIDs` → remove from pending
- SPC doesn't point at this MC → remove from pending

## Testing

- Unit tests for multi-resource capacity formula (tabular: various capacity/usage/pending combinations across dimensions)
- Unit tests for HCPResourceRequirements integration (per-HCP cost calculation)
- Unit tests for capacity strategy (current vs ceiling vs two-pass)
- Unit tests for tie-breaking (bin-packing: picks most loaded that fits)
- Integration tests with mock scheduling docs and HCPResourceRequirements
