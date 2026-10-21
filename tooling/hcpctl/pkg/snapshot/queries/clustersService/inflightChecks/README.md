# clustersService / inflightChecks

## Summary

Aggregates inflight check activity for a cluster, showing each check's lifecycle (creating, enqueued, running, passed, or error) grouped by check name in chronological order.

## What to Look For

Each inflight check should progress through creating, enqueued, running, and then pass. A healthy cluster will show all checks reaching a `passed` verb with a single occurrence of each:

| step_name                                         | verb                   | error_detail | first_occurrence             | last_occurrence              | occurrences |
|---------------------------------------------------|------------------------|--------------|------------------------------|------------------------------|-------------|
| rp-registration-inflight                          | creating missing       |              | 6/17/2026, 12:19:23.875 PM  | 6/17/2026, 12:19:23.875 PM  | 1           |
| rp-registration-inflight                          | enqueued newly created |              | 6/17/2026, 12:19:23.884 PM  | 6/17/2026, 12:19:23.884 PM  | 1           |
| rp-registration-inflight                          | running                |              | 6/17/2026, 12:19:23.914 PM  | 6/17/2026, 12:19:23.914 PM  | 1           |
| rp-registration-inflight                          | passed                 |              | 6/17/2026, 12:19:25.864 PM  | 6/17/2026, 12:19:25.864 PM  | 1           |
| cluster-managed-identities-inflight               | creating missing       |              | 6/17/2026, 12:19:23.892 PM  | 6/17/2026, 12:19:23.892 PM  | 1           |
| cluster-managed-identities-inflight               | enqueued newly created |              | 6/17/2026, 12:19:23.9 PM    | 6/17/2026, 12:19:23.9 PM    | 1           |
| cluster-managed-identities-inflight               | running                |              | 6/17/2026, 12:19:23.997 PM  | 6/17/2026, 12:19:23.997 PM  | 1           |
| cluster-managed-identities-inflight               | enqueued               |              | 6/17/2026, 12:19:28.87 PM   | 6/17/2026, 12:19:28.87 PM   | 1           |
| cluster-managed-identities-inflight               | passed                 |              | 6/17/2026, 12:19:29.817 PM  | 6/17/2026, 12:19:29.817 PM  | 1           |
| ...                                               | ...                    | ...          | ...                          | ...                          | ...         |

Watch for:
- Checks that never reach `passed` -- the cluster is stuck in the `validating` phase waiting for that check.
- Checks with `verb=error` -- the `error_detail` column will contain the failure reason.
- High `occurrences` on `running` without a corresponding `passed` -- indicates a check that is being retried repeatedly.

## Where to Go Next

- If all inflight checks passed but the cluster did not advance, review `clustersService/phases` and `clustersService/logs` for what happened after validation.
- If a check has errors, the `error_detail` column shows the Azure API response or other failure detail.
