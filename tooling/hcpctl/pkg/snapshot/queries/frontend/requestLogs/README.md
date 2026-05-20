# frontend / requestLogs

## Summary

Shows all frontend log entries for the given client request ID, including the request method, path, message, and any error.

## What to Look For

- For 4xx responses, this is the primary diagnostic output showing why the request was rejected.
- Look for error messages that explain validation failures or authorization issues.

## Where to Go Next

- If the request succeeded (2xx), check the resource-level queries for deeper diagnostics.
