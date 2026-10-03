# hypershift / managementCluster

## Summary

Resolves the AKS management cluster from events and container logs for the hosted
cluster's unique namespaces over the full snapshot time window. The management
cluster in the rendered environment config is not necessarily this resource's
placement, especially in Prow jobs with multiple management clusters.

## What to Look For

A single management cluster should be returned. It replaces the configured
management cluster for this resource's test and cleanup queries, including
per-request traces. The selected cluster is recorded in each phase manifest.

## Where to Go Next

If no placement can be discovered, the configured cluster remains the fallback.
Check `hostedClusterMetadata.md` and telemetry availability. Multiple clusters
are ambiguous and leave the fallback unchanged; inspect the returned clusters
before trusting the resource's scoped queries.
