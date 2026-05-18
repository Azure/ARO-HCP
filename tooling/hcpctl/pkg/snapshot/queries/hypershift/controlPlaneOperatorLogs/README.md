# hypershift / controlPlaneOperatorLogs

## Summary

Aggregates control-plane-operator log messages by unique msg/error pair, showing first/last occurrence and count to highlight recurring issues.

## What to Look For

Only review these logs if the `HostedCluster` or `NodePool` conditions are not sufficient to determine what the issue is. Expect to see transient errors, but any high-count repetition of errors (especially that do not resolve before deletion) are an issue.

## Where to Go Next

Check the control plane operator's pod events to see if the operator was healthy during the reconciliation.
