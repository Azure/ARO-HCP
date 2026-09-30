# hypershift / nodePoolConditionTimeline

## Summary

Extracts NodePool condition transitions from the management cluster content datadump, showing type, status, reason, and
message over time. This is the full timeline of all condition changes across all datadump emissions.

## What to Look For

The combination of condition type and status determines if the condition is expected or errant. In-progress conditions
are expected directly after creation, but the steady state should look like:

| type                            | status | reason     | message                        | lastTransitionTime   |
|---------------------------------|--------|------------|--------------------------------|----------------------|
| ValidGeneratedPayload           | True   | AsExpected | Payload generated successfully | 2026-05-15T08:29:25Z |
| ReachedIgnitionEndpoint         | True   | AsExpected |                                | 2026-05-15T08:29:29Z |
| AutorepairEnabled               | True   | AsExpected |                                | 2026-05-15T08:29:29Z |
| AllMachinesReady                | True   | AsExpected | All is well                    | 2026-05-15T08:30:05Z |
| UpdatingConfig                  | False  | AsExpected |                                | 2026-05-15T08:36:39Z |
| UpdatingVersion                 | False  | AsExpected |                                | 2026-05-15T08:36:39Z |
| AllNodesHealthy                 | True   | AsExpected | All is well                    | 2026-05-15T08:36:39Z |
| UpdatingPlatformMachineTemplate | False  | AsExpected |                                | 2026-05-15T08:36:39Z |
| Ready                           | True   | AsExpected |                                | 2026-05-15T08:36:39Z |

## Where to Go Next

If workers remain unready, check `logs/hypershift/ignitionServerLogs.md` and
`events/hypershift/ignitionServerEvents.md` for bootstrap failures in either the
HostedCluster or hosted control plane namespace.
If nodes are failing to ignite, check their boot logs.
If machines are failing to be created, check the `logs/hypershift/clusterAPILogs.md` and `logs/hypershift/clusterAPIProviderLogs.md` controller logs.
