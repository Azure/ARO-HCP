# hypershift / ignitionServerLogs

## Summary

Counts ignition requests, payload-cache lookup failures, authorization failures,
invalid payloads, and payload-cache reconciliation messages per minute and server
replica during the current phase. Searches both the HostedCluster and hosted
control plane namespaces so that ignition diagnostics are not lost when the
server resides in a different namespace from other control plane components.

This query runs for both clusters and node pools. It retains cluster-wide
activity, including sibling pools, without returning raw log bodies or tokens.
Occurrences are counted within each log record because a record can contain
multiple log lines.

## What to Look For

- Sustained `tokenLookupFailures` while workers remain unready.
- Authorization or payload validation failures that prevent bootstrap retrieval.
- Differences between replicas during the same provisioning window.
- Payload-cache reconciliation messages alongside incoming lookup failures.
  `payloadCacheHits` counts `Payload found in cache` messages, not successful HTTP
  requests.

Counts are occurrences of specific log markers, not correlated HTTP outcomes.
Request and failure lines can fall into different records or minute buckets.
Zero failure counts do not prove successful boot, and no results do not prove
the server was healthy or that its logs were available.

## Where to Go Next

Use `events/hypershift/ignitionServerEvents.md` to attribute failures to a pool
through the token Secret's object name. Request counts in this query are
cluster-wide, not per-pool or per-VM counts.

Compare failures with the NodePool condition timeline and Machine/AzureMachine
provisioning history before timeout or deletion. Inspect available VM console
logs and the ignition request handler and token reconciliation source when
deeper evidence is needed. Do not infer the token mismatch's cause from a cache
lookup failure alone.
