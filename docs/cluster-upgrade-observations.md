# Cluster upgrade observations and teardown dispatch safeguards

Phase 1 of [ARO-30519](https://issues.redhat.com/browse/ARO-30519) protects the
shared control-plane upgrade dispatch path and exports observations for later
monitoring work. It does not introduce new recording rules, alert rules,
correlation IDs, Action Groups or paging controls.

## Dispatch safeguards

Both automatic z-stream and customer-requested y-stream upgrades reach
`TriggerControlPlaneUpgrade`. The controller:

- Preserves the existing RP deletion-timestamp guard.
- Reads Cluster Service lifecycle through `GetClusterStatus`, not a full cluster
  read that can resolve unrelated autoscaler links.
- Does not post another upgrade policy after observing `uninstalling`.
- Persists that terminal observation on the primary Cluster before publishing
  the SPC condition, so SPC cleanup or a backend restart cannot erase it.
- Blocks dispatch when the lifecycle read fails or returns no state.
- Leaves the prior monitoring observation unchanged on a read failure.

The persisted `ClusterServiceUninstallingObserved` field is an internal
observation, not a deletion request or a public ARM API field. The SPC condition
`ControlPlaneUpgradeMonitoringEnabled` records the last lifecycle eligibility
observation. Its absence means no observation has been established.

These are guards on **new RP policy dispatch**. They do not cancel an accepted
policy or an upgrade already running. A lifecycle read and a policy POST are
not atomic: CS must also enforce lifecycle admission/execution if the cluster
starts uninstalling between them. Do not claim that RP checks alone guarantee
that no previously accepted upgrade can execute during teardown.

## Metric contract

| Metric | Observation |
| --- | --- |
| `backend_cluster_version_info` | Existing per-version state metric, with its labels and history-export behavior preserved |
| `backend_cluster_upgrade_target_info` | Newest observed active version, plus resolved Desired when different; value 1 for each target |
| `backend_cluster_upgrade_target_count` | Expected target-row count from the same exported snapshot |
| `backend_cluster_upgrade_converged` | 1 only when resolved Desired equals the newest observed active version and that newest entry is Completed |
| `backend_cluster_upgrade_monitoring_enabled` | Last observed lifecycle eligibility: 1 for a non-uninstalling CS state, 0 for RP deletion or observed CS uninstallation; absent if unknown |
| `backend_cluster_upgrade_inventory_size` | Expected primary-inventory rows, including malformed cache entries; absent before initial informer synchronization |
| `backend_cluster_upgrade_inventory_info` | Valid primary Cluster rows, with resource/subscription identity and `deletion_confirmed` label |
| `backend_cluster_upgrade_inventory_invalid_objects` | Number of malformed entries in the current inventory snapshot |

Target rows/count, version states, convergence and lifecycle eligibility are
collected from one immutable exporter snapshot. Reconciliation during a scrape
cannot mix its old target count with its new target rows.

The existing version-state export still reports at most three active-history
entries and emits a different Desired target when not represented. This limit
is not changed here. A missing Completed baseline or a missing metric must not
be treated as proof of health.

The lifecycle gauge is a **last observation**, not a guarantee that every
scrape performed a fresh CS lookup or that a generic CS error means upgrade
monitoring should be suppressed. The convergence gauge describes version
convergence, not ARM operation completion or general worker health.

## Independent primary inventory

The inventory collector reads one list from the primary Cluster informer cache,
independently of the SPC metrics workqueue. A disable update and SPC deletion
can coalesce before that queue observes the disable value.

The expected inventory size and valid rows come from the same cache list.
`deletion_confirmed="true"` records an RP deletion timestamp or the persisted
CS-uninstallation observation.

Malformed objects are logged and counted without failing unrelated backend
metrics. They remain in the expected total: a snapshot containing only one bad
entry reports size 1, invalid count 1 and no valid info rows, not a misleading
empty size 0. Consumers must verify counts and observation coherence before
inferring removal from an absent row.

Initial informer synchronization is required before exporting inventory.
Synchronization alone does not establish ongoing watcher freshness or make a
scrape an independent live Cosmos read.

## Exporter epochs and series churn

The new snapshot metrics use `backend_process` to separate exporter lifetimes.
Each collector/handler constructs one UUID and reuses it across scrapes and
cluster updates; it is not a per-scrape random label.

For N valid primary clusters, C SPC snapshots and T target tuples (at most 2C),
the two exporters expose N + 2 + 2C + T epoch-labelled series per current
snapshot, before external source/replica labels and normal version/lifecycle
transitions. Restarting creates new series identities. Measure restart rates,
retained cardinality and ingestion cost during observation rollout.

Raw-series retention is controlled by the monitoring workspace. A pod name or
pod UID alone does not distinguish every in-place container restart; a future
epoch replacement must preserve lifetime separation.

## Compatibility and later monitoring work

Existing recording expressions, their output names, legacy alerts and routing
remain unchanged, including the deleting-state exclusion from
[Azure/ARO-HCP#8004](https://github.com/Azure/ARO-HCP/pull/8004).
This phase does not itself fix duplicate per-version upgrade incidents.

The proposed duration budgets, cluster-level monitor, latch, missing-data
policy, clearing confirmations and alert/IcM changes belong to Phase 2 and
require review together. Phase 1 provides observations; it does not approve
those future monitoring decisions.

After SDP deployment, collect seven usable days of observations with required
warm-up and coverage checks. `alert-tester` can characterize raw signals and
simple candidate predicates. It cannot alone reconstruct undeployed,
self-referencing recording history. Keep backend/collector tests here; retain
recording-state and alert tests with their Phase 2 implementation.
