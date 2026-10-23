# Resource identity

Most queries identify the resource under investigation by its **ARM resource ID**, for example:

```
/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/<cluster>
/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/<cluster>/nodePools/<pool>
```

## Finding the ResourceID

- Take it from the objective when it is stated.
- If only the subscription and resource group are known, run `frontend/frontendRequests`
  with `ResourceGroup` (and the time window). Its `path` column holds the ARM resource ID
  of every request against the resource group.
- With only a cluster name, search `frontendLogs` with kusto_query, for example
  `| where log.resource_id has '<name>'`.

## What follows from it

Once ResourceID is known, these are derived and never need to be passed:

| Parameter | Value |
| --- | --- |
| SubscriptionID, ResourceGroup | segments of ResourceID |
| ResourceType | e.g. `microsoft.redhatopenshift/hcpopenshiftclusters/nodepools` |
| ClusterResourceID | the HCP cluster owning ResourceID (ResourceID itself for a cluster) |
| ClusterResourceName | the cluster name |
| ServiceProviderResourceType | the Cosmos service provider document type for ResourceType |

When investigating a cluster, pass either ResourceID or ClusterResourceID: ResourceID
defaults to ClusterResourceID.

## Identifiers in other systems

The cluster has further identities outside ARM. They can only be discovered by running a
query, which then provides them to later queries:

- **ClusterID**: the Clusters Service cluster ID (`cid`), used by Clusters Service, ACM,
  Maestro and cluster alert queries. Run `clustersService/cid`.
- **HostedClusterNamespace / HostedControlPlaneNamespace**: the management cluster
  namespaces (`ocm-<env>-<cid>` and `ocm-<env>-<cid>-<name>`), used by HyperShift,
  control plane and cluster API queries. Run `hypershift/hostedClusterMetadata`.
- **InternalID**: the Clusters Service internal ID path stored in the resource's Cosmos
  document. Run `backend/resourceInternalId`.

Maestro queries locate resource bundles either through ClusterID (Clusters Service logs)
or through the backend's Cosmos documents (SubscriptionID, ResourceGroup,
ClusterResourceID, ServiceProviderResourceType). Either suffices; both give a more
complete picture.
