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

## What to Look For

Normally exactly one `clusterResourceID` row. Zero rows means no HCP cluster document exists in that
subscription and resource group within the window — either the scope is wrong, or the cluster
predates or postdates the window. More than one row means the resource group holds multiple HCP
clusters; the caller must disambiguate by supplying an explicit cluster resource ID.

## Where to Go Next

The resolved `clusterResourceID` seeds `backend/cosmosResourceDiscovery` and all downstream
per-resource queries — backend state, hypershift conditions, and the velero backup queries.
