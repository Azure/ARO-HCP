# frontend / asyncOperationRequests

## Summary

Lists all frontend requests polling the async operation path, showing the status code progression over time.

## What to Look For

A client - even one that failed to see what they were expecting - should have a long string of 200-status-code responses during their polling window.

## Where to Go Next

Check the asynchronous operation resource state in the backend to see what the client saw, and the related backend resource state for the next layer in the stack.
