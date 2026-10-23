# Query library

These are the KQL queries that diagnostic snapshots are built from. Each query lives in
`<component>/<queryName>/` with:

- `query.kql`: a Go template rendered with the parameters below
- `README.md`: what the query shows, what to look for, and where to go next. READMEs
  refer to other queries by snapshot output paths such as
  `conditions/hypershift/hostedClusterConditions.md`; the last two path segments are the
  query name (`hypershift/hostedClusterConditions`)
- `query.yaml`: a description, the target database, the parameters it needs, and the
  parameters its results provide

`params.yaml` is the catalog of parameters, and `guides/` explains the important ones.

## Parameters

Parameters are shared across queries, and their values are remembered for the whole
investigation. A query's parameter value comes from, in order of precedence:

1. an argument passed to the run
2. derivation from another parameter. For example, SubscriptionID, ResourceGroup,
   ResourceType, ClusterResourceID, ClusterResourceName and ServiceProviderResourceType
   all follow from ResourceID
3. a remembered value: an earlier argument, or a value **provided** by an earlier query's
   result. A provided value is forgotten when the parameters that produced it change
4. a default. For example, PhaseStartTime/PhaseEndTime default to the investigation
   window, and ResourceID defaults to ClusterResourceID
5. empty, for optional parameters such as ServiceClusterName/ManagementClusterName

The Kusto cluster and database names are configured, never passed.

## Dependency chains

Most resource queries need only a time window (FullStartTime/FullEndTime) and a
ResourceID. Others need identifiers that only an earlier query can discover:

| To get | Run | Needs |
| --- | --- | --- |
| ResourceID or CorrelationID | `frontend/frontendRequests` (pick from its rows) | ResourceGroup |
| ClusterID | `clustersService/cid` | ClusterResourceID |
| HostedClusterNamespace, HostedControlPlaneNamespace | `hypershift/hostedClusterMetadata` | ClusterResourceID |
| ClientRequestID | `frontend/clientRequestId` | CorrelationID |
| AsyncOperationPath | `frontend/asyncOperationPath` | ClientRequestID |

So queries over Clusters Service, ACM and Maestro (via ClusterID) and over the
management cluster's control plane namespaces need `clustersService/cid` or
`hypershift/hostedClusterMetadata` to run first. Request traces need a CorrelationID or
ClientRequestID. See `guides/resource-identity.md`, `guides/requests.md` and
`guides/time-windows.md`.

Some queries only apply to one resource type (for example, node pool condition queries).
Their `query.yaml` notes say so.
