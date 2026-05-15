# maestro / agentLogs

## Summary

Filters Maestro agent container logs for entries matching the known bundle IDs or ManifestWork names, showing the agent's resource reconciliation activity.

## What to Look For

For bundle identifiers that look anomalous in the `transitions` query, filter down the query to only the pertinent logs and do a semantic analysis of the output.

## Where to Go Next

Check the maestro `events` query to see if the agent pods were experiencing any issues.
