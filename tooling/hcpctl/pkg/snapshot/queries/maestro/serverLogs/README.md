# maestro / serverLogs

## Summary

Filters Maestro server container logs for entries matching the known bundle IDs, showing the server-side processing of resource bundles.

## What to Look For

For bundle identifiers that look anomalous in the `transitions` query, filter down the query to only the pertinent logs and do a semantic analysis of the output.

## Where to Go Next

Check `events/maestro/events.md` to see if the server pods were experiencing any issues.
