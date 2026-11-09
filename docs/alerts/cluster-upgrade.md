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
| Converged | Resolved Desired equals the newest observed current version and that newest entry is Completed. An older Completed history entry is insufficient. Exported as `backend_cluster_upgrade_converged`. |
| Recovered | Convergence is confirmed by three valid observations. Historical Partial entries do not prevent recovery. Deletion/uninstallation uses a separate three-observation lifecycle-exclusion path, not upgrade success. |
| Upgrade eligible | The existing recording rule observes at least two distinct version labels and at least one Completed version for the cluster. This excludes initial installs; it is not a general cluster-health preflight. |
| Lifecycle eligible | A non-uninstalling CS lifecycle has been observed and no RP deletion timestamp has been observed. This does not mean every health or upgradeability check passed. |
| Unknown | Required observations are missing, incomplete or stale. Hold the last monitor value, whether `0` or `1`; do not manufacture an initial zero or count the gap as confirmation. |

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
| Stall clears during Pending | A phase transition puts the current target back within budget | `0` | Cancel Pending for that transient stall; completion and lifecycle clearing still use their confirmation paths below |
| Budget remains exceeded through Pending | Condition remains `1` for the full five-minute pending window | `1` | Fire one cluster-level upgrade-stuck alert |
| Phase or target changes after firing | At least one current target remains incomplete, even if a new target has a younger timer | Keep `1` | Keep the same cluster alert active; do not reset its identity or open another version-specific incident |
| Completion candidate | Fresh, coherent Desired=current+Completed observation | Keep an existing `1` on observations 1 and 2; emit `0` on observation 3 | Clear only after confirmed convergence |
| Deletion or uninstallation candidate | Fresh RP deletion, CS uninstallation, or authoritative resource-removal observation | Keep an existing `1` on observations 1 and 2; emit `0` on observation 3 | Deletion monitoring takes ownership after lifecycle exclusion; dispatch stops immediately on the first confirmed lifecycle observation |
| Observations become unavailable | The condition can no longer be reassessed | Retain the previous `0` or `1` | No state transition and no confirmation progress; missing data is not health |

Completion and lifecycle exclusion have independent counters in
`hosted_control_plane_upgrade:clear_confirmations:count`, distinguished by
`reason="completed"` or `reason="lifecycle"`. A missing observation pauses the
counter without resetting it. A valid contrary observation resets that reason's
counter to zero. Counts are bounded at three.

The group evaluates once per minute. A valid snapshot must be less than one
evaluation interval (60 seconds) old and pass all coherence/coverage checks.
Repeated evaluations of an old sample cannot count as new observations.
Three uninterrupted observations occur at the first valid evaluation and the
next two; this is an observation count, not a three-minute wall-clock timer.
Gaps can make confirmation take longer.

The established firing latch is a separate recording rule. Five prior one-minute
samples plus the current condition establish it at the first firing evaluation;
a later skipped evaluation does not require
that initial window to be satisfied again. Recomputing a healthy value requires
known state for every member of the current-target set, not just one completed
member. Missing one target therefore cannot silently resolve a firing condition.

The target list is also subject to loss. `backend_cluster_upgrade_target_count`
exports its expected size independently of the received target rows. The
backend collects count, target rows, version states and lifecycle from one
immutable snapshot, even if reconciliation updates the cluster during a scrape.
Target metrics carry `backend_process` to distinguish exporter restarts; this
label is not part of alert identity.

Before a healthy zero is allowed, every observed source must have all expected
target rows with the same scrape timestamp as its count. The count must also
match the convergence and latest version-state scrape timestamps. Missing counts, lost rows,
mixed scrape generations or an incomplete replica are unknown, not completion.
A count of zero does not establish a completed upgrade.

Source coverage is discovered independently from reported
`backend_cluster_version_info` rows, not only from the target/count/convergence
families being validated. If a source still reports an unfinished version but
loses all those families, it contributes an incomplete zero rather than
disappearing from the check. Another source's complete snapshot cannot hide it.

### Rule-evaluator outage recovery

A missed scrape while the recording group still runs is different from an
outage of the recording group itself. An evaluator outage can leave the last
monitor sample outside Prometheus's default five-minute instant lookback.

The monitor and confirmation counters recover their **latest stored value**
using an explicit 24-hour history lookup. Latest `0` or a counter reset wins
over older `1` or earlier confirmation progress; this is not a maximum-over-time
failure latch. This requires retained recordings in the same monitoring store
and supports evaluation gaps shorter than 24 hours. Missing history, store data
loss or an outage beyond that bound is not verified recovery; do not infer that
an old incident was resolved from a newly initialized value.

| Reconciliation after restoring state | Behavior |
| --- | --- |
| Fresh state is still incomplete | Keep a restored firing condition active, even for a younger target |
| Current observations are missing/incomplete | Hold the last monitor state and pause the existing confirmation counters |
| Desired=current+Completed is observed | Use the normal three-valid-observation completion check; no extra recovery timer |
| Deletion/uninstallation is observed | Use the normal three-valid-observation lifecycle check; dispatch stops immediately |
| Old clearing confirmations exist but current evidence is missing | Do not newly clear; a restored count alone is not current confirmation |
| Resource removal is confirmed | Publish the monitor's clear zero before retiring its series |

There is no separate evaluator-recovery counter or five-observation window.
The existing completion and lifecycle counters are restored too: missing
evaluations pause their progress, and valid contrary observations reset them.
Clearing requires current evidence for the same reason as well as three
confirming observations. A newly detected stall after restoring a clear monitor
still passes through the alert's normal five-minute Pending period.

Shadow review must check history availability, evaluator-outage behavior, and
the cost of retained-state queries before paging cutover. This is not a claim
that other repository monitors already use the same retained-state pattern.

| Completion edge case | Required treatment |
| --- | --- |
| Current target is Completed but stale Partial samples for the same version remain | Completed takes precedence; stale Partial samples must not retain the alert |
| Control-plane history is Completed but worker-dependent CVO history is Partial | Use independent control-plane completion when that history is available; worker lag alone does not retain this upgrade alert |
| Rollback selects a numerically older version | Use the explicit current-target set, not the highest version number |
| Desired names an older Completed history entry but newest current differs | Not converged; do not clear until Desired is actually current and Completed, confirmed three times |

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
| Primary Cluster has an RP deletion timestamp or durable uninstallation observation | Lifecycle-exclusion evidence, even if the SPC metrics queue coalesced the disable update and deletion; confirm across three valid evaluations |
| A synced, complete inventory no longer contains a previously known Cluster | Resource-removal evidence; after three valid evaluations emit a clear zero, then retire the series once remaining SPC metrics are gone |
| Initial cache synchronization is incomplete | No inventory assertion; do not infer deletion |
| Inventory rows are missing or come from a different scrape than the expected size | Incomplete observation; do not infer deletion |

`backend_cluster_upgrade_inventory_info` and `_size` are collected directly from
one primary cache snapshot rather than from the per-object metrics queue.
Counts and scrape timestamps must agree per backend process/source before
absence can prove removal. Thus a scrape outage spanning cleanup, followed by
an exporter restart, can recover from authoritative inventory without relying on
the SPC's transient final zero or its 30-second Cosmos soft-delete document.

A confirmed deletion/uninstallation immediately prevents creating another
upgrade policy. Alert clearing is separate: the monitor becomes explicitly zero
after three valid lifecycle observations, even while Partial version metrics
remain. Deletion monitoring owns stalled cleanup.

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
| Deploy backend | Current targets, their expected counts, explicit convergence and lifecycle metrics are present; deletion/uninstallation blocks dispatch; steady-state CS polling load is acceptable |
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

### Local failure-injection tests

These tests evaluate the deployment's actual recording/alert YAML with synthetic
Prometheus samples. They do not contact a cluster, change an Action Group or
raise an IcM.

| Injection | Expected local result |
| --- | --- |
| A is Completed, B is Desired, then only B's target row disappears | Monitor stays `1` and the existing alert remains firing at 46m and 55m |
| B's target row is older than its expected count | Snapshot coverage is `0`; no false recovery |
| Both target families are older than the version observations | Snapshot coverage is `0`, even when the old counts match |
| Expected target count is missing | No healthy zero; an already firing condition is retained |
| One replica is complete but another loses B's row | The complete replica cannot hide the incomplete snapshot |
| One replica loses target, count and convergence families but still reports B/Desired | Version telemetry keeps that source incomplete; the alert stays firing at 55m |
| B actually becomes current and Completed at 70m | Monitor stays `1` for the first two valid observations and becomes `0` at 72m |
| A poll is missing during completion or lifecycle confirmation | Hold the monitor and confirmation count; count resumes only on another valid observation |
| A valid contrary observation arrives during confirmation | Reset that reason's count without declaring recovery |
| The budget boundary is crossed while observations are missing | Hold an existing clear value; do not create a failure from the missing poll |
| Reconcile changes targets halfway through collection | The current scrape keeps the old count/rows/states; the next scrape has the new snapshot |
| Evaluator stops for seven minutes with a firing monitor and resumes with a younger incomplete target | Restore `1`; do not clear because the instant lookback expired |
| Evaluation resumes with incomplete current state | Keep a restored firing alert active; no new recovery timer |
| Normal completion/lifecycle confirmation reaches three after a gap | Clear through the same path used without an outage |
| Old confirmation counts exist but current clearing evidence is absent | Do not newly clear from stored counts alone |
| Stored state contains an older `1` followed by a newer `0` | Restore the latest `0`; do not resurrect the old failure |
| Resource removal overlaps evaluator recovery | Retirement waits until a confirmed zero has been published |
| Retained history approaches or reaches its 24-hour boundary | Recover within the tested bound; do not claim recovery of state that is no longer in the lookup window |

The rule cases live in
[`userJourneyClusterUpgradeMonitor-prometheusRule_test.yaml`](../../observability/alerts/userJourneyClusterUpgradeMonitor-prometheusRule_test.yaml)
and run through the generator's `run-rp-upgrade` target. Backend snapshot tests
run from the backend module with `go test ./pkg/controllers/metrics -count=1`.
The generator's
[`upgrade_monitor_regression_test.go`](../../tooling/prometheus-rules/internal/upgrade_monitor_regression_test.go)
evaluates the actual production expressions against deliberately missing
recording evaluations, including gaps longer than the instant lookback.
Local rule tests do not prove Azure receiver delivery or IcM auto-mitigation;
those remain shadow/cutover validation gates. The three-observation contract
controls the monitor's `1` to `0` transition. Azure alert auto-resolution and
downstream IcM processing may add latency; they are not timed by `promtool`.
