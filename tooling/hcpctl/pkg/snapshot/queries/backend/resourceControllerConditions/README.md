# backend / resourceControllerConditions

## Summary

Extracts condition transitions from HCP OpenShift controller resources, showing each controller's status, reason, and
message over time.

## What to Look For

Review for any `Degraded=true` values that do not resolve. Expect to see only `Degraded=false` conditions in the normal
case.

| lastTransitionTime   | controller_name            | type     | status | reason   | message      |
|----------------------|----------------------------|----------|--------|----------|--------------|
| 2026-05-15T08:27:20Z | OperationClusterCreate     | Degraded | False  | NoErrors | As expected. |
| 2026-05-15T08:27:43Z | DispatchRequestCredential  | Degraded | False  | NoErrors | As expected. |
| 2026-05-15T08:28:37Z | OperationRequestCredential | Degraded | False  | NoErrors | As expected. |
| 2026-05-15T08:41:20Z | OperationClusterDelete     | Degraded | False  | NoErrors | As expected. |

## Where to Go Next

If a controller is posting degraded status, review the controller's logs by filtering the backend:

```kql
| where log.controller_name =~ '{{ .Name }}'
```
