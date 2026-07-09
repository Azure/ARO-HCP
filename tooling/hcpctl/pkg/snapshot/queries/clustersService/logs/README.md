# clustersService / logs

## Summary

Aggregates all Clusters Service log messages for the resource by unique message, showing first/last occurrence and count to highlight recurring activity or errors.

## What to Look For

Generally verbose logs, but may exhibit repeated errors when Azure infrastructure issues occur or Maestro connection problems exist.

## Where to Go Next

- Review `events/clustersService/events.md` to confirm that Clusters Service is functioning properly.
- If logs show the destruct chain stuck at a destructor (repeated `Not continuing to the next destructor`), check `mgmtAgent/podEvents` and `mgmtAgent/podEvictions` for addon or controller pod evictions in the klusterlet namespace.
