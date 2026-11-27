# Management-cluster pressure

ARO-30244 publishes `Pressure` in the Cosmos `ManagementClusterScheduling.Status.Conditions` collection.
The singleton is `/providers/Microsoft.RedHatOpenShift/stamps/<stamp>/managementClusters/default/scheduling/default`.
In the raw Cosmos document, scheduling data is nested under `properties.status`.
This condition measures reserve at the worker pools' maximum node counts.

## Policy and accounting

For CPU, memory and `aro.openshift.io/swift-nic` independently:

```text
occupancy = max(ObservedResources.Usage, ObservedResources.Requests)
reserve % = 100 × (ScaleCeiling.Capacity − occupancy − unreflected reservations)
                  / ScaleCeiling.Capacity
```

The thresholds are **10% for each resource**. The maximum scale-ceiling age is
**five minutes**; the capacity-report age limit is **five minutes**. These are separate
code constants in `fleet/pkg/controllers/capacityreporting/pressure.go`, outside
YAML configuration. Equality meets the threshold. Negative reserve indicates pressure.

Only Swift NICs have pending reservations. Placement records the existing
per-HCP cost (non-SWIFT: zero; SWIFT SingleReplica: one; other SWIFT: three) in
`PendingSwiftNICReservations`, keyed by lowercased ARM resource ID. Only distinct
pending IDs absent from **both** ReadyResourceIDs and NotReadyResourceIDs add
this cost. Observed totals already include not-ready HCP workloads; adding their
full reservations would double-count observed demand. The report provides
aggregate demand, so this policy does not estimate remaining future demand for
partly started, already-observed HCPs. No pending CPU/memory estimates are added.
The existing conservative fallback reserves three NICs if the backend cannot
resolve a pending HCP's networking/availability details.

`Pressure=True` means at least one resource is below its own threshold.
`Pressure=False` means all three pass with valid current evidence.
`Pressure=Unknown` means the result cannot be established. Validate every input
before accepting either True or False. Missing values differ from explicit zero
usage/requests; zero scale-ceiling capacity is an invalid denominator.

The pressure controller watches scheduling changes and schedules evaluation just
after data expiry, with a one-minute periodic reconciliation as a fallback.
Collection failure invalidates recent retained values immediately when mirrored.
Unrelated Cosmos writes do not refresh measurement age. Scale-ceiling collection
polls every two minutes and refreshes `ScaleCeiling.LastReportedAt` after every
successful computation, including an unchanged numeric result. Collection errors
retain the successful payload/timestamp and publish failed scaling health.
Each worker pool must supply all resource dimensions and its applicable maximum
count; a complete pool cannot mask another pool's missing inputs.

`LastTransitionTime` changes only on transitions between True, False and Unknown.
Repeated evaluations, reason/message changes and controller restarts retain it
while status is unchanged.

## Investigating Unknown

Inspect the `Pressure` reason/message, both reporting conditions, both source
timestamps, all three resource entries in the ceiling/usage/requests maps, and
pending assignments with their NIC reservation metadata. Do not use the Cosmos
document's write time as evidence of freshness.

| Reason | Next check |
| --- | --- |
| `CapacityCollectionNotCurrent` | Inspect mirrored `CapacityDataCurrent`, the management-agent CapacityReport `ReportCurrent` condition, kube-applier mirroring and collector logs. |
| `ScalingCollectionNotCurrent` | Inspect `ScalingDataCurrent` and Fleet's scale-ceiling controller logs; check AKS resource ID, pool data and Azure API/SKU lookup failures. |
| `MissingCapacityReportTimestamp`, `MissingScaleCeilingTimestamp` | Await a successful collection; check collector versions and required input availability. |
| `InvalidCapacityReportTimestamp`, `InvalidScaleCeilingTimestamp` | Check collector clocks and future-dated reports. |
| `StaleCapacityReport`, `StaleScaleCeiling` | Check stalled collection/mirroring, controller leadership and source timestamps. A successful document write alone does not resolve this. |
| `MissingResourceData`, `InvalidResourceData` | Identify the resource in the message; check all usage/request/ceiling entries and positive ceiling capacity. |
| `InvalidPendingReservation` | Check placement and pending-cleanup controllers. During upgrade, wait for backend cleanup to backfill legacy reservations. |
| `InvalidReserveThreshold` | Correct the code policy; thresholds must be in the range 0–100. |

Recovery requires valid current data; changing a reason or rewriting a document
does not establish recovery.
