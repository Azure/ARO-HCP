# Version alerts after a rollback

This review covers [ARO-29828](https://issues.redhat.com/browse/ARO-29828). A
version pin can hold a cluster at an older z-stream while the fleet's best
version is newer. That steady state is intentional and should not page. A
rollback that fails to start or complete should still page.

| Alert | Signal | Pinned older version after completion | Rollback still running |
| --- | --- | --- | --- |
| `userJourneyClusterUpgradeStuckInDesired` | Target has remained `desired` without becoming `partial` for about 25 minutes | Quiet: the target is already `completed` | Fires if the target never becomes active |
| `userJourneyClusterUpgradeStuckInProgress` | Target has remained `desired` or `partial` without becoming `completed` for about 35 minutes | Quiet: a completed target is excluded | Fires if the target does not complete |
| `HCPClusterVersionFailing` | The CVO reports `Failing` for one hour with no other unavailable or degraded operator explaining it | Quiet when CVO is healthy | Fires if CVO reports an independent failure during rollback or afterward |

The two journey alerts read `backend_cluster_version_info`. They require two
distinct observed version labels and a completed version, so an initial install
is excluded. Their rules do not compare the target with the fleet's best
version or compare z-stream numbers. `PinnedVersion` is not a label on this
metric. While a rollback is underway, the older target can be `desired` or
`partial` and is monitored exactly like a newer target. Once it reaches
`completed`, the duration series disappears. The backend emits `desired` only
when that version is not already active, so holding a completed older version
does not create a new desired series.

`HCPClusterVersionFailing` reads `cluster_operator_conditions` from the hosted
cluster. It has no pin or fleet-version signal. Suppressing it whenever a pin
exists could hide a real CVO failure, such as a failed payload fetch or a
rollback precondition error. A confirmed false positive would need evidence
that CVO reports `Failing` solely because the completed version is below the
fleet's best version; the current rules and synthetic tests cannot establish
that CVO behavior. Check live CVO conditions if that case is observed.

The two journey alerts keep their existing `alertname`, `correlationId`,
recording-rule names, and SLO labels. Their descriptions now say explicitly
that they cover rollback. Renaming them would be clearer, but requires checking
alert routing, silences, dashboards, and queries maintained outside this
repository first. Promtool tests exercise a blocked rollback, a completed
rollback, and the stable completed state.
