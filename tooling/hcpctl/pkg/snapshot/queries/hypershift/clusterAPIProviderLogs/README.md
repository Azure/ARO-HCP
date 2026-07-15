# hypershift / clusterAPIProviderLogs

## Summary

Aggregates CAPI Azure provider controller log messages, showing first/last occurrence and count per unique message/error/controller/resource combination.

## What to Look For

This query shows the interaction with Azure to create and manage the VMs that back node pools. Look for:

- Azure API errors (e.g. quota exceeded, resource not found, throttling)
- Machines failing to provision or being deleted unexpectedly
- Long gaps between reconciliation attempts indicating controller issues

## Where to Go Next

If Azure-level errors are found, check the Azure activity logs for the resource group. For machine lifecycle issues, check `clusterAPILogs` for the upstream CAPI controller perspective.
