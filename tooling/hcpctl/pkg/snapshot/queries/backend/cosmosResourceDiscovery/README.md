# backend / cosmosResourceDiscovery

## Summary

Discovers all resource types that exist in Cosmos DB under the cluster's ARM resource ID prefix.
This drives which per-resource queries are applicable (e.g., controller conditions only fire when
`hcpopenshiftcontrollers` child documents exist).

`cosmosResourceSnapshots` is a change feed, so time scoping depends on the seed mode. Request-seeded
(`from-resource`) runs scope discovery to the snapshot window (`timestamp between FullStartTime ..
FullEndTime`), matching the ARM-request activity that seeded them. Identity-seeded (`from-cluster`)
runs instead scan all history up to the window end (`timestamp <= FullEndTime`), so an idle cluster
whose resources last changed before the window is still discovered.

Because the identity-seeded scan spans all history, it collapses each resource to its latest snapshot
(`arg_max` on `content._ts`) and drops resources whose latest snapshot is soft-deleted
(`content.deletionTimestamp` set). This keeps the inventory to what currently exists rather than
everything that ever existed, so resources deleted or replaced before the window are not queried as
if present. Request-seeded runs keep the window-scoped `distinct` unchanged.

## What to Look For

A list of `(resourceID, resourceType)` pairs showing all Cosmos documents related to this cluster.
Resource types with `/hcpopenshiftcontrollers` suffixes indicate controller condition data is available.
Resource types with `/readdesires` indicate management cluster state is available. Resource types with
`/serviceprovider*` indicate service provider state documents exist.

## Where to Go Next

This query is informational — its results drive the availability of downstream queries like
`backend/resourceControllerConditions`, `backend/serviceProviderState`, and `hypershift/hostedClusterMetadata`.
