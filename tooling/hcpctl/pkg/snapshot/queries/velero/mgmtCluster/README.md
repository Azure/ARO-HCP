# velero / mgmtCluster

## Summary

Discovers which management (AKS) cluster(s) host this HCP's velero backups, by reading the
`cluster` from the HCP's Velero Backup CRs (matched by the ARM resource-id annotation) —
exactly the management cluster where velero runs for this HCP. Velero runs per management
cluster, so this list scopes the cross-HCP `logs/velero/serverLogs.md` query. Discovery-only
— its result feeds other queries and is not primary triage output.

## What to Look For

Normally a single management cluster. More than one means the control plane moved
between management clusters during the window (a migration or failover), which is worth
noting when reading velero results.

## Where to Go Next

`logs/velero/serverLogs.md` — shared velero server errors/warnings on these clusters.
