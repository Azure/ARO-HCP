# hypershift / hostedClusterConditions

## Summary

Extracts a point-in-time snapshot of HostedCluster conditions from the *last* datadump emission within
the current phase's time window. In the `test/` phase this shows the cluster's condition state just before
cleanup began; in the `cleanup/` phase it shows the final state before the overall time window ended.

The phase boundaries are derived from test timing metadata — see the phase's `manifest.json` for the
exact `start` and `end` timestamps used to scope this query.

Unlike `hostedClusterConditionTimeline`, which shows every condition transition over time, this query
shows only the final state of each condition — making it easy to see whether the cluster was healthy
before deletion.

## What to Look For

A healthy cluster should have conditions like:

| type      | status | reason     | message                               | lastTransitionTime   |
|-----------|--------|------------|---------------------------------------|----------------------|
| Degraded  | False  | AsExpected | The hosted cluster is not degraded    | 2026-05-15T08:25:43Z |
| Available | True   | AsExpected | The hosted control plane is available | 2026-05-15T08:41:55Z |

**Pay specific attention to the `Available` condition, as it gives the best summary of top-level status.**

## Where to Go Next

- Check `conditions/hypershift/hostedClusterConditionTimeline.md` for the full history of condition changes.
- If `Degraded` or `Available` names a control plane component, review `events/hypershift/controlPlaneEvents.md` and filter by that pod prefix (for example `etcd-`, `kube-apiserver-`).
- Check the control plane operator's pod events to see if the operator was healthy during reconciliation.
