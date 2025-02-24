# Asynchronous Operations in the ARO-HCP Resource Provider

This document aims to capture the design decisions that went into the asychronous operations implementation in the ARO-HCP frontend and backend components.

It assumes the reader has a basic understanding of ARM's asynchronous operations contract. The [Azure Resource Manager Resource Provider Contract](https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/async-api-reference.md)<sup>(RPC)</sup> is the best reference for this.

> [!NOTE]
> Links to the Resource Provider Contract have a <sup>(RPC)</sup> superscript and require a Microsoft Enterprise Managed User (EMU) GitHub account – aka `b-yourname@microsoft.com`

The first thing to say is the design (especially the backend's design) is less than optimal due to several constraints:

1. Cluster Service currently has no update notification mechanism for clients. This means polling is required to get status updates on asynchronous operations in progress, and polling introduces latency in propagating status updates to end users and updating Azure billing records.
  
2. Microsoft's official [Azure SDK for Go](https://github.com/Azure/azure-sdk-for-go) is lagging behind Microsoft's Azure SDK for other languages like [.NET](https://github.com/azure/azure-sdk-for-net) and [Python](https://github.com/Azure/azure-sdk-for-python) in its Cosmos DB support.  The Go SDK is missing two key features:
   - No [change feed](https://learn.microsoft.com/en-us/azure/cosmos-db/change-feed) support, not even [limited support for the pull model only](https://github.com/Azure/azure-sdk-for-go/issues/21686). This means, once again, polling. Polling Cosmos DB for new Azure subscriptions and polling for new asynchronous operations. Again, more latency. (This could be addressed with backend enhancements, which we'll discuss later.)

   - [No support for cross-partition queries](https://github.com/Azure/azure-sdk-for-go/issues/18578). This limits the design to single-partition queries requiring a partition key. It also makes the set of partition keys in use undiscoverable without some kind of out-of-band workaround (which we'll also discuss later).

## Cosmos DB Data Model

If you're not accustomed to schema-free databases, [Data modeling in Azure Cosmos DB](https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/modeling-data) is a recommended read.

I'm no Cosmos DB expert, but I have picked up a few nuggets of wisdom from experience.

The first thing is finding the right partitioning strategy in a Cosmos DB container is critical, especially when limited (as we are) to single-partition queries.  Advanced Cosmos DB features like [stored procedures](https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/how-to-write-stored-procedures-triggers-udfs) and especially [transactional batch operations](https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/transactional-batch) are limited in scope to a single logical partition within a single container.

The second thing is documents in a Cosmos DB container – aside from a few [system-defined fields](https://learn.microsoft.com/en-us/rest/api/cosmos-db/documents) – are free-form JSON blobs. Unlike with tables in a schema-based database, [documents within a Cosmos DB container do not have to be homogeneous](https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/modeling-data#distinguish-between-different-document-types). This is worth taking advantage of considering the aforementioned scoping constraints.

With these points in mind, ARO-HCP has settled on the following data model.

### Containers

The ARO-HCP resource provider interacts with the following containers:

#### Resources

This is the primary container where all the registered Azure subscriptions, hosted control plane (HCP) cluster and node pool metadata, and asynchronous operation tracking that the ARO-HCP resource provider is reponsible for lives.

The container is partitioned by Azure subscription ID and has a default [Time-to-Live (TTL)](https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/time-to-live) of -1, meaning documents within this container by default do not automatically expire, but some do.

Documents in this container consist of an outer "envelope" with fixed fields, and an inner "payload" whose fields are dictated by the resource type declared in the "envelope" section.

##### Resources Envelope

Let's take a closer look at the outer "envelope" of a document in the "Resources" container:
```
{
    "id":            <uuid>,                  (1)
    "partitionKey":  <Azure subscription ID>, (2)
    "resourceType":  <Azure resource type>,   (3)
    "properties": {                           (4)
        ... payload section ...
    },
    "ttl":           <optional, in seconds>,  (5)
    "_etag" and other system-generated fields
}
```
1. The `id` field is a lowercased [128-bit UUID](https://rfc-editor.org/rfc/rfc4122.html) (e.g. "`xxxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`"), generated by the RP when necessary. Depending on the resource type, the `id` value may be significant.

   For Azure subscriptions, the `id` is the Azure subscription ID (and therefore matches the `partitionKey` field).

   For asynchronous operations, the `id` is the asynchronous operation ID.  It is also the last path segment of the endpoint URL(s) returned to ARM in the `Location` and/or `Azure-AsyncOperation` response headers.

   The `id` value is _not_ significant for hosted control plane clusters and node pools.

2. The `partitionKey` field is an Azure subscription ID, which itself is a [128-bit UUID](https://rfc-editor.org/rfc/rfc4122.html) (e.g. "`xxxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`"), also lowercased.
    
3. The `resourceType` field is a lowercased Azure resource type, including the namespace.

   For example, a document for an Azure subscription will have a `resourceType` value of "`microsoft.resources/subscriptions`".
    
4. The `properties` field is the "payload" section. Its content depends on the envelope's resource type. More on this below.

5. The `ttl` (time-to-live) field is only used for asynchronous operation documents, which automatically expire after 7 days.

   See the Azure documentation: [Time to Live (TTL) in Azure Cosmos DB](https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/time-to-live)
 
##### Azure Subscriptions

Resource type:
* `microsoft.resources/subscriptions`

It's worth noting that because the "Resources" container is partitioned by Azure subscription ID, there is only one Azure subscription document per logical partition.

The "payload" section for an Azure subscription document is the full, verbatim content of a subscription PUT request. The [Resource Provider Contract](https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/subscription-lifecycle-api-reference.md#request)<sup>(RPC)</sup> has all the details about the content stored here. The top-level `state` field is the only field that's significant to the ARO-HCP resource provider as the subscription state influences the resource provider's behavior in request handling and implicit resource cleanup within the subscription.

##### Hosted Control Plane Clusters and Node Pools

Resource types:
* `microsoft.redhatopenshift/hcpopenshiftclusters`
* `microsoft.redhatopenshift/hcpopenshiftclusters/nodepools`

The primary purpose of a hosted control plane cluster or node pool document is to serve as a cross-reference from Azure's to OpenShift Cluster Manager (OCM)'s resource identifier.

The "payload" section of the document is as follows:
```
{
    "resourceId":        <Azure resource ID>                    (1)
    "internalId":        <OpenShift Cluster Manager API path>   (2)
    "activeOperationId": <ID of active async operation, if any> (3)
    "provisioningState": <status of last async operation>       (4)
    "systemData":        <Azure system metadata>                (5)
    "tags":              <Azure resource tags>                  (6)
}
```
<a name="resource-document-resourceid-field"></a>

1. The `resourceId` field is the Azure resource ID for the hosted control plane cluster or node pool. It is also the ARO-HCP resource provider's API path for the resource:
   ```
   /subscriptions/{subscription_id}/resourceGroups/{resource_group_name}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{cluster_name}[/nodePools/{node_pool_name}]
   ```
   The casing is preserved from the original PUT request that created the resource, so we use a case-insensitive string match when querying for the resource ID.

<a name="resource-document-internalid-field"></a>

2. The `internalId` field is the OpenShift Cluster Manager's API path for the hosted control plane cluster or node pool:
   ```
   /api/clusters_mgmt/v1/clusters/{cluster_id}[/node_pools/{node_pool_id}]
   ```
   Within the ARO-HCP resource provider we call this the "internal ID" since it is hidden from the end-user.

> [!NOTE]
> The ARO-HCP resource provider is gradually transitioning from the `/api/clusters_mgmt/v1` OCM endpoint to a new `/api/aro_hcp/v1alpha1` endpoint. The OCM endpoint used to create the resource will be reflected in the `internalId` field.

3. The `activeOperationId` field refers to the `id` field of the Cosmos DB document representing the current asynchronous operation acting on the resource. In Azure there can only be one active operation on a resource at a time. This field is cleared when the active asynchronous operation reaches a terminal state.

<a name="resource-document-provisioningstate-field"></a>

4. The `provisioningState` field stays syncrhonized with the [`status` field](#operation-document-status-field) of the Cosmos DB document representing the active asynchonrous operation. The reason the value is duplicated here is to preserve the last operation's terminal status, potentially beyond the [limited lifespan](#operation-document-time-to-live) of asynchronous operation documents.

5. The `systemData` field persists the content of the most recent ARM-provided `x-ms-arm-resource-system-data` HTTP request header for the resource.

   This is a [requirement for all Azure resource providers](https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/common-api-contracts.md#system-metadata-for-all-azure-resources)<sup>(RPC)</sup>:
   > The systemData object should be defined in the resource swagger and persisted in provider storage to serve on all responses for the resource (GET, PUT, PATCH).
   
   Cluster Service does not handle Azure system metadata so the ARO-HCP resource provider must persist it.

6. The `tags` field persists the set of [user-provided tags for the resource](https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources), which is just a key/value map.

   All "tracked" resource types in Azure (which include hosted control plane clusters and node pools) are required to support resource tagging. Cluster Service does not handle Azure resource tags so the ARO-HCP resource provider must persist it.

##### Asynchronous Operations

Resource type:
* `microsoft.redhatopenshift/hcpoperationsstatus`

<a name="operation-document-time-to-live"></a>

Asynchronous operation documents automatically expire after 7 days, by way of the [system-defined time-to-live ("ttl") field](https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/time-to-live). By extension, asynchronous operation status endpoints returned to ARM in the `Location` and/or `Azure-AsyncOperation` response headers also expire after 7 days since failure to find the operation document in Cosmos DB results in a "404 Not Found" HTTP response.

The "payload" section of an asynchronous operation document includes all the information needed to respond to status requests from ARM on the endpoint returned through the `Azure-AsyncOperation` response header. The response body format for these status requests is described in the [Resource Provider Contract](https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/async-api-reference.md#azure-asyncoperation-resource-format)<sup>(RPC)</sup>. The "payload" section does not follow the response body format exactly. Instead, it has the following fields:
```
{
    "tenantId":           <Azure tenant ID>                    (1)
    "clientId":           <Azure client ID>                    (2)
    "request":            "Create"|"Update"|"Delete"           (3)
    "externalId":         <Azure resource ID>                  (4)
    "internalId":         <OpenShift Cluster Manager API path> (5)
    "operationId":        <operation status endpoint, if any>  (6)
    "notificationUri":    <async operation callback URI>       (7)
    "startTime":          <RFC 3339 timestamp>                 (8)
    "lastTransitionTime": <RFC 3339 timestamp>                 (9)
    "status":             <provisioning state>                 (10)
    "error":              <OData error, if any>                (11)
}
```

1. The `tenantId` field records the tenant ID of the subscription from which the operation was initiated. The value is copied from the `x-ms-home-tenant-id` request header and is used to validate access to the operation's status endpoint.

   The field is only set for explicitly requested asynchronous operations. See "Explicit vs Implicit Operations" below.

2. The `clientId` field records the object ID of the client Java Web Token (JWT) that initiated the operation. The value is copied from the `x-ms-client-object-id` request header and is used to validate access to the operation's status endpoint.

   The field is only set for explicitly requested asynchronous operations. See "Explicit vs Implicit Operations" below.

3. The `request` field captures the nature of the operation. Valid values are "Create", "Update", and "Delete".

4. The `externalID` field is the same as the [`resourceID` field](#resource-document-resourceid-field) in hosted control plane cluster and node pool documents.

5. The `internalID` field is the same as the [`internalID` field](#resource-document-internalid-field) in hosted control plane cluster and node pool documents.

6. The `operationID` field is the status endpoint returned to ARM in the `Azure-AsyncOperation` response header.

   The field is only set for explicitly requested asynchronous operations. See "Explicit vs Implicit Operations" below.

7. The `notificationUri` field is for ARM's [Async Operation Callbacks](https://eng.ms/docs/products/arm/api_contracts/asyncoperationcallback) protocol. This is an opt-in ARM feature that ARO-HCP has not yet onboarded to as of this writing, but the API contract has been implemented nonetheless. The value is copied from the `Azure-AsyncNotificationUri` request header, if present.

8. The `startTime` field is an [RFC 3339](https://www.rfc-editor.org/rfc/rfc3339.html) formatted UTC timestamp marking the start of the operation.

9. The `lastTransitionTime` field is an [RFC 3339](https://www.rfc-editor.org/rfc/rfc3339.html) formatted UTC timestamp marking the most recent change to the `status` field. When the operation status becomes terminal (`Succeeded`, `Failed`, or `Canceled`), the `lastTransitionTime` field marks the end of the operation since there will be no further `status` changes.

<a name="operation-document-status-field"></a>

10. The `status` field uses the same set of values as the [`provisioningState` field](#resource-document-provisioningstate-field) in hosted control plane cluster and node pool documents, with one exception: if a "Delete" operation supersedes an active "Update" operation, the "Update" operation's `status` field becomes `Canceled`.

11. The `error` field contains the structured error section of the [operation resource format](https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/async-api-reference.md#azure-asyncoperation-resource-format)<sup>(RPC)</sup>. This is set when the operation status becomes `Failed` or `Canceled`. See [Error Response Content](https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/common-api-details.md#error-response-content)<sup>(RPC)</sup> for more details about the error structure.

## Asynchronous Operation Flow

The best way to illustrate how asynchronous operations are handled by the ARO-HCP resource provider is to walk through a few examples. First, however, it's important to understand the roles of the ARO-HCP frontend and backend pods with respect to asynchronous operations.

The frontend pods collectively serve as a load-balanced endpoint for communication with the Azure Resource Manager (ARM). When a request from ARM arrives that requires asynchronous handling, the frontend pod will initiate an asynchronous operation by creating an [asynchronous operation](#asynchronous-operations) document in Cosmos DB. As ARM then begins polling for status updates on the operation, the frontend pods will read back the asynchronous operation document from Cosmos DB and convert it to the response format ARM expects.

That's the extent of what the frontend pods do with asynchronous operation documents. The rest is handled by the lead backend pod.

The lead backend pod periodically iterates over all registered Azure subscriptions and looks for any asynchronous operation documents in Cosmos DB with a non-terminal status.  It then queries Cluster Service for the current status of the resource the operation is acting on.  If the backend receives an updated status for the resource, it updates the asynchronous operation document.

Now let's walk through a few concrete examples.

### Create an HCP OpenShift Cluster



... FINISH ME ...
