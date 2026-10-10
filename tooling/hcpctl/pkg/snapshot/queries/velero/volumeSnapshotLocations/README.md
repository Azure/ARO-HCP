# velero / volumeSnapshotLocations

## Summary

Snapshot history of the Velero VolumeSnapshotLocation (VSL) CRs on the management cluster(s)
hosting this HCP, one row per captured snapshot event. Like BackupStorageLocations, VSLs are
shared, cluster-scoped Velero config and carry NO per-HCP ARM resource-id annotation, so they
cannot be pinned to a single HCP. The result is scoped to the management cluster(s) that host
this HCP (`ManagementClusterNames`, discovered by `velero/mgmtCluster`). `isHealthy` flags the
`Available` phase.

## What to Look For

- `phase` — a misconfigured or unavailable VSL breaks the CSI-snapshot / data-mover path that
  DataUploads depend on.
- `provider` / `config` — the snapshot provider and its settings; unexpected values point at a
  misconfigured location.

## Where to Go Next

- `state/velero/dataUploads.md` — per-volume upload failures behind a `PartiallyFailed` backup
  when the VSL is unhealthy.
- `state/velero/backups.md` — backups that partially failed on volume snapshotting.
