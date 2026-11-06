# Cluster upgrade stuck monitor

Tracks [ARO-30519](https://issues.redhat.com/browse/ARO-30519).

`userJourneyClusterUpgradeStuck` reports one upgrade condition per cluster.
Its identity uses service cluster, ARM resource ID, and subscription; version,
phase, and UUID observation changes do not create new alert identities. Versions remain
available in `backend_cluster_version_info`, the per-version duration recording
rules, and `backend_cluster_upgrade_target_info`.

## Monitor contract

### Definitions

| Term | Definition used by this monitor |
| --- | --- |
| Current targets | The newest observed active version, plus the resolved desired version if different. These are exported by `backend_cluster_upgrade_target_info`. Selecting `.6` while `.5` is still observed as running does not remove `.5` from this set. |
| Historical target | An older active-history entry that is not a current target. Its Partial state remains diagnostic evidence but does not create or retain a separate upgrade alert. |
| Desired | The selected target is not yet reflected in the reported active versions. |
| Partial | The target has not completed. For a current target this can mean an ongoing or stalled rollout; for a historical target it can mean an abandoned attempt. |
| Completed | Successful rollout reported by the selected version-history source. Prefer `HostedCluster.status.controlPlaneVersion.history`; otherwise use legacy `status.version.history`. ARM `Succeeded` is not this signal. |
| Stuck | A current target exceeds an applicable timing budget. After the full pending window, the cluster condition stays active while a current target remains incomplete, including after selecting a younger target. |
| Recovered | All current targets are observed as Completed. Historical Partial entries do not prevent recovery. Deletion/uninstallation clears the monitor for a different reason: it is no longer applicable. |
| Upgrade eligible | The existing recording rule observes at least two distinct version labels and at least one Completed version for the cluster. This excludes initial installs; it is not a general cluster-health preflight. |
| Lifecycle eligible | A non-uninstalling CS lifecycle has been observed and no RP deletion timestamp has been observed. This does not mean every health or upgradeability check passed. |
| Unknown | Required observations are unavailable. Unknown is not a numeric monitor value and must not be interpreted as a successful `0`. An existing `1` is retained. |

### Timing and monitor values

The recording rule emits `hosted_control_plane_upgrade:stuck:bool`; the alert
fires only after that value has remained `1` for a further five minutes.

| Check | Clock starts | Budget exceeded when | Initial monitor result |
| --- | --- | --- | --- |
| Reach Partial | Target first observed as eligible Desired | Target remains Desired for **more than 20 minutes**, without Partial or Completed | `1`; alert enters Pending, normally firing after approximately 25 minutes |
| Reach Completed | Earliest eligible Desired or Partial observation for that target | Target remains incomplete for **more than 30 minutes** | `1`; alert enters Pending, normally firing after approximately 35 minutes |
| No stuck condition | Current observations are available and neither budget has been exceeded | Not applicable | `0`; no upgrade alert |

The completion clock includes time spent in Desired: becoming Partial does
**not** start another 30-minute window. For example, Desired at 00:00 and
Partial at 00:10 reaches the completion threshold just after 00:30, not 00:40.
These clocks use monitoring observations, not the ARM request timestamp.

Existing eligibility and diagnostic cardinality are preserved: the backend
reports at most three active-history entries. Verify that a Completed baseline
is visible during shadow validation, especially for deeper Partial histories.
No upgrade alert is not, by itself, proof that the cluster is healthy.

### State-transition process

| Step or transition | Observed condition | Monitor value | Alert/incident behavior |
| --- | --- | --- | --- |
| Await initial observations | Lifecycle or current-target observations are not yet available, with no previous stuck condition | Unknown/no sample | Do not manufacture a healthy `0` or infer upgrade success |
| Initial install | Cluster is not upgrade eligible; target and lifecycle observations are available | `0` | No upgrade-stuck alert |
| Upgrade progressing within budget | Cluster is eligible; current targets are within both applicable budgets | `0` | No upgrade-stuck alert |
| First budget exceeded | A current target exceeds either timing threshold | `1` | Pending for five minutes; not yet a firing upgrade alert |
| Stall clears during Pending | No current target still exceeds a budget, for example after completion or reaching Partial within the completion budget | `0` | Cancel Pending; no upgrade incident is raised for that transient stall |
| Budget remains exceeded through Pending | Condition remains `1` for the full five-minute pending window | `1` | Fire one cluster-level upgrade-stuck alert |
| Phase or target changes after firing | At least one current target remains incomplete, even if a new target has a younger timer | Keep `1` | Keep the same cluster alert active; do not reset its identity or open another version-specific incident |
| Current targets complete | All current targets are Completed; historical Partial entries may remain | `0` | Clear the condition and use the normal 1-to-0 auto-mitigation flow |
| Deletion or uninstallation confirmed at any stage | RP deletion timestamp is set, or CS reports `uninstalling` | `0`, regardless of retained version metrics | Cancel Pending or clear Firing; deletion monitoring takes ownership. This is lifecycle exclusion, not upgrade success |
| Observations become unavailable | An existing stuck condition can no longer be reassessed | Retain the previous `1` | Do not auto-mitigate merely because telemetry disappeared or a lifecycle lookup failed |

The established firing latch is a separate recording rule. Five prior one-minute
samples plus the current condition establish it at the first firing evaluation;
a later skipped evaluation does not require
that initial window to be satisfied again. Recomputing a healthy value requires
known state for every member of the current-target set, not just one completed
member. Missing one target therefore cannot silently resolve a firing condition.

| Completion edge case | Required treatment |
| --- | --- |
| Current target is Completed but stale Partial samples for the same version remain | Completed takes precedence; stale Partial samples must not retain the alert |
| Control-plane history is Completed but worker-dependent CVO history is Partial | Use independent control-plane completion when that history is available; worker lag alone does not retain this upgrade alert |
| Rollback selects a numerically older version | Use the explicit current-target set, not the highest version number |

## Lifecycle and dispatch

`TriggerControlPlaneUpgrade` observes CS lifecycle on every normal reconcile,
including clusters already at their desired version. It uses `GetClusterStatus`,
not a full cluster read that can resolve unrelated autoscaler links. It mirrors
`ControlPlaneUpgradeMonitoringEnabled` onto ServiceProviderCluster conditions.
The metrics handler exports that observation as
`backend_cluster_upgrade_monitoring_enabled`: 1 for an observed non-uninstalling
CS state, 0 for an RP deletion timestamp or CS `uninstalling`.

The terminal CS uninstallation observation is also persisted as
`Cluster.ServiceProviderProperties.ClusterServiceUninstallingObserved` before
publishing the SPC condition. This is an observation, not a deletion request,
and survives SPC cleanup and backend restarts.

| Independent primary inventory signal | Recovery meaning |
| --- | --- |
| Primary Cluster has an RP deletion timestamp or durable uninstallation observation | Confirmed lifecycle exclusion, even if the SPC metrics queue coalesced the disable update and deletion |
| A synced, complete inventory no longer contains a previously stuck Cluster | Confirmed resource removal; emit a clear zero, then retire its monitor series |
| Initial cache synchronization is incomplete | No inventory assertion; do not infer deletion |
| Inventory rows are missing or come from a different scrape than the expected size | Incomplete observation; do not infer deletion |

`backend_cluster_upgrade_inventory_info` and `_size` are collected directly from
one primary cache snapshot rather than from the per-object metrics queue.
Counts and scrape timestamps must agree per backend process/source before
absence can prove removal. Thus a scrape outage spanning cleanup, followed by
an exporter restart, can recover from authoritative inventory without relying on
the SPC's transient final zero or its 30-second Cosmos soft-delete document.

A confirmed deletion/uninstallation both prevents creating another upgrade
policy and makes the cluster monitor explicitly zero, even while Partial
version metrics remain. Deletion monitoring owns stalled cleanup.

CS read errors or missing lifecycle state prevent dispatch and preserve the
last observation for monitoring; they are not recovery. Other CS failure states
are not newly excluded by this change. This is not a general health preflight,
an upgrade-serialization policy, or a worker-health gate.

There is an unavoidable interval between observing lifecycle and posting a
policy. CS must also enforce lifecycle admission to reject a concurrent
uninstall; the RP check alone is not atomic.

## Rollout and review

Set `monitoring.clusterUpgradeMonitorMode` in the scoped configuration. It is
passed through the monitoring Bicep parameters to the RP rule wrapper. The
default is `legacy`; this PR does not automatically enable new paging.

| Mode | Legacy detectors | Shadow detector | Production detector | Delivery |
| --- | --- | --- | --- | --- |
| `legacy` | Enabled | Disabled | Disabled | Existing legacy routing is unchanged |
| `shadow` | Enabled | Enabled | Disabled | Shadow has no Action Groups, including no Event Hub delivery; legacy still pages |
| `active` | Disabled | Disabled | Enabled | New production detector uses existing RP routing |

| Step | Required evidence before promotion |
| --- | --- |
| Deploy backend | Current targets and lifecycle metrics are present; deletion/uninstallation blocks dispatch; steady-state CS polling load is acceptable |
| Deploy recording rules, then select `shadow` | New inputs are available before evaluating the new detector; old alerting remains enabled |
| Validate shadow behavior | Known stuck clusters produce 1; completion or confirmed deleting/uninstalling produces 0; missing observations do not mitigate; phase/version/UUID changes do not duplicate the cluster condition |
| Prepare cutover | Review old active incidents, routing, silences, old SLO labels and version-specific correlation IDs; agree how to dispose of old incidents; separately review any dashboard/UI changes with required screenshots |
| Select `active` | Verify that the production detector starts its pending/firing lifecycle, reaches the RP receiver, and auto-mitigates on a confirmed 1-to-0 transition |
| Expand by environment/region | Reviewer-approved observations support widening the rollout; the mode can be returned to `legacy` for detector rollback |

Legacy, shadow and production groups have different Azure resource identities.
The production detector is not evaluated before `active`, rather than attaching
an Action Group to an already-firing shadow alert and assuming a notification
will be replayed. Both new detectors use the same recording rule and alert
definition. Other RP rules and the shared Action Group are unaffected.

The old `userJourneyClusterUpgradeStuckInDesired` and
`userJourneyClusterUpgradeStuckInProgress` detectors retain their expressions,
labels and correlation IDs during `legacy` and `shadow`. They do not gain the
new lifecycle-clear behavior. They remain deployed but are disabled in `active`.
Existing incidents do not inherit the new correlation key, and disabling a rule
must not be assumed to close its existing incidents.

This is a phased deployment through SDP pipelines, not a production deployment
performed by the PR. Metric-only preparation has precedent in
[Azure/ARO-HCP#7767](https://github.com/Azure/ARO-HCP/pull/7767); the repository's
[Alert Verification guide](../alert-verification.md) also recommends testing
against historical data before rollout.

Rule tests cover current completion with retained historical Partial series,
pending and firing lifecycle transitions, stable identity across stages and
targets, rollback, initial-install exclusion, selective telemetry loss,
evaluation gaps, coalesced SPC deletion and scrape outages/exporter restart.
This PR does not modify the Grafana dashboard; its text/layout changes require
a separate screenshot-backed review.
