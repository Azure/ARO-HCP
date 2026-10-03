# velero / deleteBackupRequests

## Summary

Backup-deletion requests for this HCP — deletion / garbage-collection intent and outcome.
A durable record complementing the backup snapshot stream: a DeleteBackupRequest captures a
deletion that a Backup `event: "Delete"` row may have missed. Matched to this HCP via the
`velero.io/backup-name` label, whose value embeds the HCP namespace token.

## What to Look For

- `phase == Processed` with non-empty `errors` — the deletion failed; the backup (and its
  object-store data) may be orphaned.
- Unexpected deletions of backups you still expect to exist.
- `backup` names correlate each request to a row in `state/velero/backups.md`.

## Where to Go Next

- `state/velero/backups.md` — whether the referenced backup still appears afterwards.
- `logs/velero/serverLogs.md` — velero server errors around the deletion.
