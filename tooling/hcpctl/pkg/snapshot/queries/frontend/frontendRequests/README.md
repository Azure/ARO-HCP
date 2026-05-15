# frontend / frontendRequests

## Summary

Summarizes all ARM requests in the resource group, grouped by correlation ID, method, path, and status code.

## What to Look For

Pay close attention to mutating calls, as these expose the most important actions taken by clients.

## Where to Go Next

Review the async operation requests to confirm that clients waited correctly on operation state; review the async operation state to see what client saw during their polling; view related resource state in the backend to dig into the next layer of the stack.
