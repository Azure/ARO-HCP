# hypershift / hostedClusterConditions

## Summary

Extracts HostedCluster condition transitions from the management cluster content datadump, showing type, status, reason,
and message over time.

## What to Look For

The combination of condition type and status determines if the condition is expected or errant. In-progress conditions
are expected directly after creation, but the steady state should look like:

| type      | status | reason     | message                               | lastTransitionTime   |
|-----------|--------|------------|---------------------------------------|----------------------|
| Degraded  | False  | AsExpected | The hosted cluster is not degraded    | 2026-05-15T08:25:43Z |
| Available | True   | AsExpected | The hosted control plane is available | 2026-05-15T08:41:55Z |

Try filtering for `type` matching `*ClusterVersion*` when investigating upgrades.

**Pay specific attention to the `Available` condition, as it gives the best summary of top-level status.**

## Where to Go Next

Check the control plane operator's pod events to see if the operator was healthy during the reconciliation.