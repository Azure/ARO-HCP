# backend / serviceProviderState

## Summary

Shows the service provider resource document state over time from the backend datadump, ordered by etag. This is the internal (non-customer-facing) data used for record-keeping by the backend.

## What to Look For

Each row is a distinct record of the object, refine the query to pick out specific fields to see how they evolve over time.

## Where to Go Next

For clusters, review the backend cluster resource conditions or the HyperShift `HostedCluster` conditions to go to the next layer of the stack.
For node pools, review the backend node pool resource conditions or the HyperShift `NodePool` conditions to go to the next layer of the stack.