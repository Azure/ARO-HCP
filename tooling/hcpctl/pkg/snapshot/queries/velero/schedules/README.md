# velero / schedules

## Summary

Snapshot history of this HCP's Velero Schedule CRs — the cron definitions that drive
backup creation. Pinned to this HCP via the `azure.microsoft.com/hcp-cluster-azure-resource-id`
annotation. Answers the first triage question: *is the HCP being backed up at all?*

## What to Look For

- `paused == true` — backups are suspended; no new Backup CRs will be created.
- `phase != Enabled` (`isHealthy == false`) or non-empty `validationErrors` — the schedule
  itself is broken.
- `cron` cadence vs `lastBackup` — a stale `lastBackup` relative to the cron means backups
  stopped firing.
- No rows at all — no schedule exists for this HCP.

## Where to Go Next

`state/velero/backups.md` — the Backup CRs this schedule should have produced.
