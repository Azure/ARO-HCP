# velero / backupStorageLocations

## Summary

Snapshot history of the Velero BackupStorageLocation (BSL) CRs on the management cluster(s)
hosting this HCP, one row per captured snapshot event. BSLs are shared, cluster-scoped Velero
config (the ARO backup controller points every HCP Backup at StorageLocation `default`), so
they carry NO per-HCP ARM resource-id annotation and cannot be pinned to a single HCP. The
result is scoped to the management cluster(s) that host this HCP (`ManagementClusterNames`,
discovered by `velero/mgmtCluster`). `isHealthy` flags the `Available` phase.

## What to Look For

- `phase` — a BSL stuck in `Unavailable` (bad credentials, unreachable object store) fails
  every backup on that management cluster, so this is often the root cause of failures across
  all HCPs on the cluster.
- `message` — the validation error explaining an `Unavailable` phase.
- `lastValidationTime` / `lastSyncedTime` — stale timestamps mean Velero stopped validating or
  syncing the location.
- `provider` / `bucket` / `prefix` — the object-store target; unexpected values point at a
  misconfigured location.

## Where to Go Next

- `state/velero/backups.md` — did the backups on this cluster fail while the BSL was
  `Unavailable`?
- `logs/velero/serverLogs.md` — velero server errors validating or syncing the location.
