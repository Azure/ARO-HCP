# frontend / asyncOperationRequests

## Summary

Lists all frontend requests polling the async operation path, showing the status code progression over time.

## What to Look For

A client - even one that failed to see what they were expecting - should have a long string of 200-status-code responses during their polling window.

## Where to Go Next

Check `state/backend/asyncOperationState` in the request directory to see what the client saw, and `state/backend/resourceState` in the resource directory for the next layer in the stack.
