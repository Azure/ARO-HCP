# clustersService / provisionSteps

## Summary

Aggregates provision step activity for a cluster, showing each step's lifecycle (running, succeeded, error, or not yet completed) grouped by step name in chronological order.

## What to Look For

Each provision step should progress from running to succeeded. Steps are re-run on each reconciliation loop, so `occurrences > 1` is normal. A healthy cluster will show all steps eventually reaching `succeeded`:

| step_name                                                              | verb                   | error_detail | first_occurrence            | last_occurrence             | occurrences |
|------------------------------------------------------------------------|------------------------|--------------|-----------------------------|-----------------------------|-------------|
| managed-resource-group-provision-step                                  | running                |              | 6/17/2026, 12:19:36.919 PM | 6/17/2026, 12:22:41.094 PM | 17          |
| managed-resource-group-provision-step                                  | succeeded              |              | 6/17/2026, 12:19:49.339 PM | 6/17/2026, 12:22:41.409 PM | 17          |
| run-arm-helper-step                                                    | running                |              | 6/17/2026, 12:19:49.339 PM | 6/17/2026, 12:22:41.409 PM | 17          |
| run-arm-helper-step                                                    | succeeded              |              | 6/17/2026, 12:19:49.339 PM | 6/17/2026, 12:22:41.409 PM | 17          |
| ...                                                                    | ...                    | ...          | ...                         | ...                         | ...         |
| control-plane-identities-role-assignment-on-managed-resource-group-step | running                |              | 6/17/2026, 12:19:52.407 PM | 6/17/2026, 12:22:41.421 PM | 17          |
| control-plane-identities-role-assignment-on-managed-resource-group-step | error                  | GET https://management.azure.com/.../roleAssignments/356ccc48-... | 6/17/2026, 12:19:52.728 PM | 6/17/2026, 12:19:55.307 PM | 2 |
| control-plane-identities-role-assignment-on-managed-resource-group-step | error                  | PUT https://management.azure.com/.../roleAssignments/c35ea678-... | 6/17/2026, 12:20:01.274 PM | 6/17/2026, 12:20:01.274 PM | 1 |
| control-plane-identities-role-assignment-on-managed-resource-group-step | succeeded              |              | 6/17/2026, 12:20:26.693 PM | 6/17/2026, 12:22:42.737 PM | 5           |
| ...                                                                    | ...                    | ...          | ...                         | ...                         | ...         |
| node-pools-egress-public-ip-provision-step                             | running                |              | 6/17/2026, 12:21:41.466 PM | 6/17/2026, 12:22:54.535 PM | 5           |
| node-pools-egress-public-ip-provision-step                             | has not completed yet  |              | 6/17/2026, 12:21:46.369 PM | 6/17/2026, 12:21:46.369 PM | 1           |
| node-pools-egress-public-ip-provision-step                             | succeeded              |              | 6/17/2026, 12:22:00.434 PM | 6/17/2026, 12:22:54.535 PM | 4           |
| ...                                                                    | ...                    | ...          | ...                         | ...                         | ...         |
| hosted-cluster-provision-step                                          | running                |              | 6/17/2026, 12:22:39.624 PM | 6/17/2026, 12:22:56.226 PM | 2           |
| hosted-cluster-provision-step                                          | has not completed yet  |              | 6/17/2026, 12:22:41.029 PM | 6/17/2026, 12:22:41.029 PM | 1           |
| hosted-cluster-provision-step                                          | succeeded              |              | 6/17/2026, 12:22:57.605 PM | 6/17/2026, 12:22:57.605 PM | 1           |
| set-cluster-to-installing-state-step                                   | running                |              | 6/17/2026, 12:22:57.605 PM | 6/17/2026, 12:22:57.605 PM | 1           |
| set-cluster-to-installing-state-step                                   | succeeded              |              | 6/17/2026, 12:22:57.638 PM | 6/17/2026, 12:22:57.638 PM | 1           |

The example above shows a healthy cluster where transient authorization errors on the role assignment step were retried and eventually succeeded, and long-running async steps (`node-pools-egress-public-ip`, `tls-certificates`, `hosted-cluster`) showed `has not completed yet` before succeeding.

Watch for:
- Steps with `verb=error` -- the `error_detail` column will contain the Azure API response or other failure detail.
- Steps with `verb=running` but no corresponding `succeeded` -- the cluster is stuck at that provision step.
- Steps with `verb=has not completed yet` followed by `succeeded` -- normal for long-running steps, but high `occurrences` may indicate slowness.
- High `occurrences` on `error` for the same step -- indicates a step that is failing repeatedly before eventually succeeding (or not).

## Where to Go Next

- If a step has errors, the `error_detail` column shows the Azure API response. Common issues include authorization failures (403) and quota exhaustion.
- If all provision steps succeeded but the cluster did not reach `installing`, review `clustersService/phases` and `clustersService/logs` for what happened after provisioning.
- If the cluster reached `installing` but not `ready`, review `conditions/hypershift/hostedClusterConditions` for the next layer of the stack.
