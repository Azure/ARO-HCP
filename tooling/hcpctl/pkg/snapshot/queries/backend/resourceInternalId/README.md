# backend / resourceInternalId

## Summary

Resolves the Clusters Service internal ID for a resource from the backend datadump, linking the ARM resource to its CS representation.

Identity-seeded (`from-cluster`) runs scan `cosmosResourceSnapshots` history up to the window end rather than within the window, since it is a change feed — otherwise an idle cluster with no in-window changes yields no internal ID and every dependent `clustersService/*` query returns nothing.

## What to Look For

A URI referring to the Clusters Service API related to this document.

## Where to Go Next

If this does not exist, check `conditions/backend/resourceControllerConditions.md` to see why a Clusters Service document was not created.
