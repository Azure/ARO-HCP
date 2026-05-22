# hypershift / nodePoolConditions

## Summary

Extracts a point-in-time snapshot of NodePool conditions from the *last* datadump emission before
cleanup/deletion began. When a cleanup start time is available, this shows the node pool's condition state
just before teardown; otherwise it uses the test end time.

Unlike `nodePoolConditionTimeline`, which shows every condition transition over time, this query
shows only the final state of each condition — making it easy to see whether the node pool was healthy
before deletion.

## What to Look For

A healthy node pool should have conditions like:

| type                            | status | reason     | message                        | lastTransitionTime   |
|---------------------------------|--------|------------|--------------------------------|----------------------|
| ValidGeneratedPayload           | True   | AsExpected | Payload generated successfully | 2026-05-15T08:29:25Z |
| AllMachinesReady                | True   | AsExpected | All is well                    | 2026-05-15T08:30:05Z |
| Ready                           | True   | AsExpected |                                | 2026-05-15T08:36:39Z |

## Where to Go Next

- Check `nodePoolConditionTimeline` for the full history of condition changes.
- If ignition is failing, check the ignition server logs in the hosted control plane's namespace.
- If machines are failing to be created, check the `logs/hypershift/clusterAPILogs.md` and `logs/hypershift/clusterAPIProviderLogs.md` controller logs.
