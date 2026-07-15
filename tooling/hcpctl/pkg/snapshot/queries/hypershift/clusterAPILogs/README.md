# hypershift / clusterAPILogs

## Summary

Aggregates Cluster API (CAPI) controller log messages for machine-related controllers, showing first/last occurrence and count per unique message/error/controller/resource combination.

## What to Look For

This query provides an overview of the Machine lifecycle (creation, health checks, deletion) for the VMs that make up the user's node pools. Look for:

- Repeated errors in machine provisioning or health checks
- Machines stuck in a particular state (e.g. pending, deleting)
- Unexpected controller reconciliation patterns

## Where to Go Next

If machine-level issues are found, check `clusterAPIProviderLogs` for the corresponding Azure resource management activity, and `nodePoolConditions` for the node pool's status conditions.

If machines do not pass health checks, check to make sure the `ignition` server was healthy or see if the VM boot diagnostic logs are available.