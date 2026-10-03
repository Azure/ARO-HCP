# velero / dataUploads

## Summary

Per-volume data-mover upload operations (CSI snapshot -> object store) for this HCP, where
PersistentVolume backup failures surface (e.g. etcd data volumes). DataUpload CRs carry no
ARM annotation, so they are matched by `spec.sourceNamespace` against this HCP's namespaces
(`ocm-<env>-<id>` and `-<name>`). `isFailed` flags Failed / Canceled uploads.

## What to Look For

- `phase == Failed` / `Canceled` (`isFailed`) — the volume upload did not complete; this is
  the usual cause behind a `PartiallyFailed` backup.
- `bytesDone` stalled well below `totalBytes` — an upload that hung.
- `sourcePVC` / `sourceNamespace` to identify which volume failed; `node` for where the
  data-mover pod ran (correlate with node problems).

## Where to Go Next

- `state/velero/backups.md` — the parent Backup (`backup` column) this upload belongs to.
- `logs/velero/logs.md` — data-mover pod logs (the per-backup pod names embed the backup name).
