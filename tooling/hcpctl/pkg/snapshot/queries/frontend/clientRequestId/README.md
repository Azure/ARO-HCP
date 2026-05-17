# Client Request ID Discovery

## Summary

Resolves the `client_request_id` from a `correlation_request_id` when only the
latter is available. A single correlation ID may map to multiple client request
IDs (one per ARM request in the correlation group); this query returns all of
them.

## What to Look For

- Multiple rows indicate the correlation ID spans several ARM requests. Each
  will be traced independently.
- Zero rows means the correlation ID was not seen in the frontend logs during
  the time window.

## Where to Go Next

- `logs/frontend/asyncOperationRequests.md` — polling history for each request
