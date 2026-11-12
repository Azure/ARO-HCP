# hypershift / ignitionServerLogs

## Summary

Groups ignition-server messages and errors by replica across both discovered namespaces, including sibling pools, with first/last occurrence and record count. Plain-text records, including multiline content, are retained when no structured message exists.

## What to Look For

Repeated errors or replica differences while workers remain unready. No message allowlist is applied; counts represent records, not requests. Empty results do not prove healthy bootstrap.

## Where to Go Next

Use `events/hypershift/ignitionServerEvents.md` for pool attribution, then compare the NodePool timeline and available VM boot logs.
