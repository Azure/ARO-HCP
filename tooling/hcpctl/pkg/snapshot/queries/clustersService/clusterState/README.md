# clustersService / clusterState

## Summary

Shows Clusters Service cluster objects observed by the backend client, including GET, create, update, and list results. Every successful observation is logged, even when unchanged. The csstatedump controller supplies a 30-minute backfill through the same client. Historical controller-only snapshots are also included.

## What to Look For

Each row is a distinct record of the object, refine the query to pick out specific fields to see how they evolve over time.

## Where to Go Next

Review `conditions/hypershift/hostedClusterConditions.md` to go to the next layer of the stack.
