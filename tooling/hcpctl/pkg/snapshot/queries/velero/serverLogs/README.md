# velero / serverLogs

## Summary

Velero **server** error/warning logs on the management cluster(s) hosting this HCP (from
`discovery/velero/mgmtCluster.md`, or the PR-job hint). Deliberately **not** filtered to
this HCP: the velero server is shared per management cluster, so an infrastructure-level
failure (object-store auth, plugin crash, BackupStorageLocation unavailable) breaks backups
for every HCP on the cluster and carries no per-HCP token. Aggregated by `cluster`, `level`,
`msg`, and extracted `err`.

## What to Look For

- Repeated `error`-level `msg` / `err` spanning the failure window — a shared-plane problem
  affecting all HCPs on that management cluster.
- BackupStorageLocation / object-store / credential errors — these explain backups that fail
  before any per-HCP work starts.

## Where to Go Next

- `logs/velero/logs.md` — the same window filtered to this HCP's namespaces (server +
  node-agent + data-mover), to tell shared failures from HCP-specific ones.
- `state/velero/backups.md` — whether backup phases correlate with these server errors.
