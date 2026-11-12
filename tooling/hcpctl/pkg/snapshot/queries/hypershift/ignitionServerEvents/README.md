# hypershift / ignitionServerEvents

## Summary

Groups ignition-server events by object, reason, and message across both discovered namespaces, including sibling pools. All event reasons are retained.

## What to Look For

Look for repeated failures, such as `Token not found in cache`, `Bad header`, or `Token invalid`. Token Secret names identify pools/configurations, not individual VMs. `maxRecordedEventCount` is cumulative, not window-local; `observedRecords` counts logged records, not failed requests. Observation timestamps do not establish onset or resolution.

## Where to Go Next

Compare `logs/hypershift/ignitionServerLogs.md`, the NodePool timeline, and Machine/AzureMachine provisioning before timeout or deletion. Empty results do not establish successful bootstrap.
