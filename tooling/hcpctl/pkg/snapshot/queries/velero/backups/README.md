# velero / backups

## Summary

Full snapshot history of this HCP's Velero Backup CRs, captured on the management cluster,
one row per captured snapshot event so phase transitions are visible over time. Pinned to
this HCP via the `azure.microsoft.com/hcp-cluster-azure-resource-id` annotation (a
management cluster hosts many HCPs). `isFailed` flags failed / partially-failed phases.

## What to Look For

- `phase` progression `New -> InProgress -> Completed`. Terminal `Failed`,
  `PartiallyFailed`, or `FailedValidation` (see `isFailed`) means the backup did not
  fully succeed.
- `errors` / `warnings` counts and `failureReason` / `validationErrors` for the cause.
- Gaps in `backup` rows vs the schedule cadence — backups that never started.

## Where to Go Next

- `state/velero/schedules.md` — is a schedule producing these backups at all?
- `state/velero/dataUploads.md` — per-volume upload failures behind a `PartiallyFailed`.
- `logs/velero/logs.md` — velero component logs for this HCP around the failure time.
