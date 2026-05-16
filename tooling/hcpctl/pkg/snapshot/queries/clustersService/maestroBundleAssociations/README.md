# clustersService / maestroBundleAssociations

## Summary

Extracts Maestro bundle creation events from Clusters Service logs, mapping bundle IDs and names to the Kubernetes
resources they contain, including the group/version and kind of each resource.

## What to Look For

An overview of the Maestro bundles created, with the resource GVK and identifiers contained therein:

| bundleId | bundleName | resource | groupVersion                          | resourceKind       |
|----------|------------|----------|---------------------------------------|--------------------|
| uuid     | uuid       | ns/name  | work.open-cluster-management.io/v1    | ManifestWork       |
| uuid     | uuid       | ns/name  | multitenancy.acn.azure.com/v1alpha1   | PodNetwork         |
| uuid     | uuid       | ns/name  | multitenancy.acn.azure.com/v1alpha1   | PodNetworkInstance |
| uuid     | uuid       | ns/name  | cluster.open-cluster-management.io/v1 | ManagedCluster     |
| uuid     | uuid       | ns/name  | work.open-cluster-management.io/v1    | ManifestWork       |
| uuid     | uuid       | ns/name  | work.open-cluster-management.io/v1    | ManifestWork       |

## Where to Go Next

If this query returns no values, review `logs/clustersService/logs` to see what went wrong.
