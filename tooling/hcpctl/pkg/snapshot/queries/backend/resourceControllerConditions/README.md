# backend / resourceControllerConditions

## Summary

Extracts a point-in-time snapshot of HCP OpenShift controller conditions from the *last* datadump emission within
the current phase's time window. In the `test/` phase this shows each controller's condition state just before
cleanup began; in the `cleanup/` phase it shows the final state before the overall time window ended.

The phase boundaries are derived from test timing metadata — see the phase's `manifest.json` for the
exact `start` and `end` timestamps used to scope this query.

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

- Check `conditions/backend/resourceControllerConditionTimeline.md` for the full history of condition changes.
- If `OperationClusterCreate` is degraded or ARM create stays `Provisioning`, review `OperationClusterCreate` rows here and `conditions/hypershift/hostedClusterConditions.md` for the degraded component message.
- If a controller is posting degraded status, review the controller's logs by filtering the backend.
