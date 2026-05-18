# maestro / mgmtAuditLogs

## Summary

Extracts Kubernetes audit log entries from the management cluster for ManifestWork resources, showing who mutated them and with what result.

## What to Look For

Mutating calls for resources covered by Maestro bundles should result from agent spec application and result in agent status publication. The number of mutating calls to a resource should roughly resemble layers 4,5,6 in the `transitions` query.

## Where to Go Next

If we expected to see mutating calls on an object but didn't, it depends on which object:

- `HostedCluster` or contents in the Hosted Cluster namespace: review the HyperShift `HostedCluster` conditions and controller logs
- `*PodNetwork*`: the class of network-related CRDs are owned by Azure networking and SWIFT, review the events for these objects and otherwise raise an IcM to get logs from the Azure team