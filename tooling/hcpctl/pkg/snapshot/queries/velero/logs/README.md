# velero / logs

## Summary

Velero component logs (server, node-agent, and per-backup data-mover pods, all in the
`velero` namespace on the management cluster) attributed to this HCP by matching its
namespace tokens in the log body. Aggregated by `container_name`, `level`, and `msg` with
first/last occurrence and count — raw per-line output would be far too large for a snapshot.

## What to Look For

- `level == error` / `warning` rows, highest `occurrences` first, around a failing backup's
  time window (from `state/velero/backups.md`).
- `container_name` distinguishes the emitting component: `velero` (server),
  `node-agent`, or a per-backup data-mover pod.
- A repeating `msg` spanning `first_occurrence`..`last_occurrence` — a persistent failure vs
  a one-off.

## Where to Go Next

- `state/velero/backups.md` / `state/velero/dataUploads.md` — correlate error times to a
  specific backup or upload.
- `logs/velero/serverLogs.md` — shared velero-server errors not attributable to one HCP.
