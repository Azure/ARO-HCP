# Cluster upgrade observations and teardown dispatch safeguards

Phase 1 of [ARO-30519](https://issues.redhat.com/browse/ARO-30519) protects the
shared control-plane upgrade dispatch path and exports observations for later
monitoring work. It does not introduce new recording rules, alert rules,
correlation IDs, Action Groups or paging controls.

## Dispatch safeguards

Both automatic z-stream and customer-requested y-stream upgrades reach
`TriggerControlPlaneUpgrade`. The controller:

- Preserves the existing RP deletion-timestamp guard.
- Treats `ClusterServiceDeletionTimestamp` as sufficient local teardown evidence
  too; neither deletion timestamp requires a CS probe.
- Reads Cluster Service lifecycle through `GetClusterStatus`, not a full cluster
  read that can resolve unrelated autoscaler links, only for a dispatch candidate
  after initialization, create-operation and desired/current gates.
- Does not poll CS or initialize an Active condition for converged clusters.
- Does not post another upgrade policy after observing `uninstalling`.
- Persists that observation on the primary Cluster before publishing
  the SPC condition, so SPC cleanup or a backend restart cannot erase it.
- Revalidates it on a later dispatch candidate when neither local deletion
  timestamp is set. A successful non-uninstalling observation clears an obsolete
  marker and recovers the SPC condition before dispatch.
- Blocks dispatch when that lifecycle read fails or returns no state.
- Leaves the prior monitoring observation unchanged on a read failure.

The persisted `ClusterServiceUninstallingObserved` field is an internal
observation, not a deletion request or a public ARM API field. The SPC condition
`ControlPlaneUpgradeMonitoringEnabled` records the last lifecycle eligibility
observation after a disable/recovery transition. Absence implies eligibility
without claiming a fresh CS observation. An initial successful non-uninstalling
probe does not write True; True is written only to recover an existing
condition. This avoids an initial fleet write storm. A CS blip does not affect
converged/no-work reconciliations because they do not call CS.

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
| `backend_cluster_upgrade_monitoring_enabled` | Last/implied eligibility: an absent SPC condition defaults to 1, False emits 0, and explicit Unknown emits no sample |
| `backend_cluster_upgrade_inventory_size` | Expected primary-inventory rows, including malformed cache entries; absent before initial informer synchronization |
| `backend_cluster_upgrade_inventory_info` | One gauge per valid Cluster: 1 for RP deletion, dispatched CS deletion or persisted CS-uninstallation observation; otherwise 0 |
| `backend_cluster_upgrade_inventory_invalid_objects` | Number of malformed entries in the current inventory snapshot |

Target rows/count, version states, convergence and lifecycle eligibility are
collected from one immutable exporter snapshot. Reconciliation during a scrape
cannot mix its old target count with its new target rows.

The existing version-state export still reports at most three active-history
entries and emits a different Desired target when not represented. This limit
is not changed here. A missing Completed baseline or a missing metric must not
be treated as proof of health.

The lifecycle gauge is a **last or implied observation**, not a guarantee that every
scrape performed a fresh CS lookup or that a generic CS error means upgrade
monitoring should be suppressed. The convergence gauge describes version
convergence, not ARM operation completion or general worker health.

## Independent primary inventory

The inventory collector reads one list from the primary Cluster informer cache,
independently of the SPC metrics workqueue. A disable update and SPC deletion
can coalesce before that queue observes the disable value.

The expected inventory size and valid rows come from the same cache list.
The inventory gauge value records teardown without changing the identity
labels. A transition from 0 to 1 does not create a new time series.
Both 0 and 1 rows count toward inventory completeness; 0 means a present
non-teardown row, not an absent resource.

Malformed objects are logged and counted without failing unrelated backend
metrics. They remain in the expected total: a snapshot containing only one bad
entry reports size 1, invalid count 1 and no valid info rows, not a misleading
empty size 0. Consumers must verify counts and observation coherence before
inferring removal from an absent row. Error summaries are rate-limited to one
per five minutes per collector; every scrape still exports the invalid count.

Initial informer synchronization is required before exporting inventory.
Synchronization alone does not establish ongoing watcher freshness or make a
scrape an independent live Cosmos read.

## Exporter epochs and series churn

Metrics registration creates one `backend_process` UUID and passes that same
value to both upgrade collectors. They reuse it across scrapes and updates;
it is not a per-scrape or per-collector random label.

All new upgrade metrics, including monitoring eligibility, carry that epoch.
`backend_cluster_version_info` deliberately keeps its existing labels so the
deployed legacy recording/alert contracts do not change. This is an explicit
compatibility boundary, not a claim that the legacy metric has the epoch.

Join new metrics at backend/source scope with `backend_process`,
`resource_id`, `subscription_id` and `cluster_uuid` where applicable. Target
rows additionally carry `version`; inventory does not carry `cluster_uuid`.
Validate counts and timestamps per source before deduplication. To correlate
the legacy version metric, ignore only `backend_process` and explicitly
validate source/UUID/version and scrape coherence. Do not join it on an epoch
label it does not have or silently treat incomplete sources as success.

For N valid primary clusters, C SPC snapshots and T target tuples (at most 2C),
the two exporters expose up to N + 2 + 3C + T epoch-labelled series per current
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

The saved Phase 2 proposal needs adaptation to this reviewed raw contract
before submission: inventory presence counts both gauge values, teardown
selects value 1 and new lifecycle metrics share the registration epoch.
This phase does not modify that preservation branch.

After SDP deployment, collect seven usable days of observations with required
warm-up and coverage checks. `alert-tester` can characterize raw signals and
simple candidate predicates. It cannot alone reconstruct undeployed,
self-referencing recording history. Keep backend/collector tests here; retain
recording-state and alert tests with their Phase 2 implementation.
