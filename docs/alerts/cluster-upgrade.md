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

| Completion edge case | Required treatment |
| --- | --- |
| Current target is Completed but stale Partial samples for the same version remain | Completed takes precedence; stale Partial samples must not retain the alert |
| Control-plane history is Completed but worker-dependent CVO history is Partial | Use independent control-plane completion when that history is available; worker lag alone does not retain this upgrade alert |
| Rollback selects a numerically older version | Use the explicit current-target set, not the highest version number |

## Lifecycle and dispatch

`TriggerControlPlaneUpgrade` observes CS lifecycle on every normal reconcile,
including clusters already at their desired version. It mirrors
`ControlPlaneUpgradeMonitoringEnabled` onto ServiceProviderCluster conditions.
The metrics handler exports that observation as
`backend_cluster_upgrade_monitoring_enabled`: 1 for an observed non-uninstalling
CS state, 0 for an RP deletion timestamp or CS `uninstalling`.

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

1. Deploy the backend first and verify target and lifecycle metrics are present.
2. Deploy the recording and alert rules together. The old
   `userJourneyClusterUpgradeStuckInDesired` and
   `userJourneyClusterUpgradeStuckInProgress` alerts are replaced, not retained.
3. Check external routing, silences, dashboards, and automation for those names,
   their old SLO labels, and version-specific correlation IDs. Existing incidents
   do not automatically inherit the new correlation key.
4. Confirm the standard monitor 1-to-0 auto-mitigation flow with reviewers,
   including a firing-to-uninstalling transition.

Rule tests cover current completion with retained historical Partial series,
pending and firing lifecycle transitions, stable identity across stages and
targets, rollback, initial-install exclusion, and missing observations.
