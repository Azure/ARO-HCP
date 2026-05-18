# frontend / frontendRequests

## Summary

Summarizes all ARM requests in the resource group, grouped by correlation ID, method, path, and status code.

## What to Look For

Pay close attention to mutating calls, as these expose the most important actions taken by clients.

## Where to Go Next

For each request, review `logs/frontend/asyncOperationRequests.md` to confirm that clients waited correctly on operation state; view `state/backend/resourceState.md` in the resource directory to dig into the next layer of the stack.
