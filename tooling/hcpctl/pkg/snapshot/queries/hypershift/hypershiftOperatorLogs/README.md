# hypershift / hypershiftOperatorLogs

## Summary

Aggregates HyperShift operator log messages for the target HostedCluster or NodePool by unique msg/error pair, showing first/last occurrence and count.

## What to Look For

Only review these logs if the `HostedCluster` or `NodePool` conditions are not sufficient to determine what the issue is. Expect to see transient errors, but any high-count repetition of errors (especially that do not resolve before deletion) are an issue.

## Where to Go Next

Check the HyperShift operator's pod events to see if the operator was healthy during the reconciliation.
