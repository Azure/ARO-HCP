# hypershift / ignitionServerEvents

## Summary

Lists `GetPayloadFailed` events emitted by the ignition server during the current
phase in either the HostedCluster or hosted control plane namespace. Runs for
both clusters and node pools and retains failures from sibling pools.

## What to Look For

- `Token not found in cache`, indicating an incoming token's payload lookup
  failed.
- `Bad header` or `Token invalid`, indicating authorization or decoding failures.
- Token Secret object names identifying the affected node pools and configuration
  hashes, without exposing Secret data.

`firstObserved` and `lastObserved` are timestamps of event records observed in
the selected window, not necessarily the event's original onset or resolution.
`maxRecordedEventCount` is the largest cumulative Kubernetes Event count seen;
it is not the number of failures that occurred within this window.
`observedRecords` counts logged event records, not individual failed requests.
No results do not establish successful bootstrap.

## Where to Go Next

Compare these events with `logs/hypershift/ignitionServerLogs.md` and the
NodePool condition timeline. Trace Machine/AzureMachine provisioning before
timeout or deletion. Pool attribution does not identify the individual VM
responsible for each failed request.
