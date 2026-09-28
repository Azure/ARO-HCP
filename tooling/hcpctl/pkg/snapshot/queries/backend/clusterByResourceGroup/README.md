# backend / clusterByResourceGroup

## Summary

Resolves the HCP cluster's ARM resource ID from a subscription plus resource group. It scans
`cosmosResourceSnapshots` for documents of type `Microsoft.RedHatOpenShift/hcpOpenShiftClusters`
that live in the given resource group and returns their distinct resource IDs.

This is the ARM-request-INDEPENDENT discovery seed used by the `from-cluster` snapshot entrypoint.
Unlike `backend/cosmosResourceDiscovery` — which needs a cluster resource ID prefix that has
already been discovered from frontend ARM request logs — this query needs only the subscription and
resource group. It therefore works for an idle cluster that had no ARM traffic during the snapshot
window.

Because `cosmosResourceSnapshots` is a change feed (a row is emitted only when a document changes),
identity resolution scans all history up to the window end (`timestamp <= FullEndTime`) rather than
within the window. A cluster created before the window with no in-window changes still resolves.

Since the scan spans all history, it collapses each cluster to its latest snapshot
(`arg_max` on `content._ts`) and drops any whose latest snapshot is soft-deleted (the producer marks
deletes with `content.deletionTimestamp`, mirroring the live `NOT IS_DEFINED(c.deletionTimestamp)`
storage filter). Without this a resource group that once held a now-deleted cluster would resolve a
stale id or report multiple historical ids as ambiguous.

## What to Look For

Normally exactly one `clusterResourceID` row. Zero rows means no live HCP cluster document exists in
that subscription and resource group at or before the window end — either the scope is wrong, the
cluster was created after the window, or its latest snapshot is soft-deleted. More than one row means
the resource group holds multiple live HCP clusters; the caller must disambiguate by supplying an
explicit cluster resource ID.

## Where to Go Next

The resolved `clusterResourceID` seeds `backend/cosmosResourceDiscovery` and all downstream
per-resource queries — backend state, hypershift conditions, and the velero backup queries.
