# backend / resourceControllerConditions

## Summary

Extracts a point-in-time snapshot of HCP OpenShift controller conditions from the *last* datadump emission before
cleanup/deletion began. When a cleanup start time is available, this shows each controller's condition state
just before teardown; otherwise it uses the test end time.

Unlike `resourceControllerConditionTimeline`, which shows every condition transition over time, this query
shows only the final state of each controller — making it easy to see whether controllers were healthy
before deletion.

## What to Look For

Review for any `Degraded=true` values. Expect to see only `Degraded=false` conditions in the normal case.

| lastTransitionTime   | controller_name            | type     | status | reason   | message      |
|----------------------|----------------------------|----------|--------|----------|--------------|
| 2026-05-15T08:27:20Z | OperationClusterCreate     | Degraded | False  | NoErrors | As expected. |
| 2026-05-15T08:27:43Z | DispatchRequestCredential  | Degraded | False  | NoErrors | As expected. |

## Where to Go Next

- Check `resourceControllerConditionTimeline` for the full history of condition changes.
- If a controller is posting degraded status, review the controller's logs by filtering the backend.
