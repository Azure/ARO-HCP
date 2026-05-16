# backend / resourceState

## Summary

Shows the full resource document state over time from the backend datadump, ordered by etag to reveal each mutation. This is the external (customer-facing) data used storing the ARM resource clients see.

## What to Look For

Each row is a distinct record of the object, refine the query to pick out specific fields to see how they evolve over time.

## Where to Go Next

For clusters, review `conditions/backend/resourceControllerConditions` or `conditions/hypershift/hostedClusterConditions` to go to the next layer of the stack.
For node pools, review `conditions/backend/resourceControllerConditions` or `conditions/hypershift/nodePoolConditions` to go to the next layer of the stack.