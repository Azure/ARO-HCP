# Image Registry Operator Identity

This operator has **two** distinct components that both end up holding Azure
credentials: the **operator** itself, which runs as a control plane
component and talks to the Azure Resource Manager (control plane) API, and
the **registry** (the operand it manages), which runs on worker nodes and
talks to the Azure Blob **data plane** API directly.

## Why These Two Components Exist

The `cluster-image-registry-operator` is a controller: it watches the
`configs.imageregistry.operator.openshift.io/cluster` object and reconciles
storage, RBAC, routes, and most relevantly here — the `Deployment` for the
actual registry server. It never serves an image pull or push itself.

The registry `Deployment` it creates (the "operand") runs
[`docker-registry`](https://github.com/openshift/image-registry)/
[`distribution`](https://github.com/distribution/distribution), the process
that actually receives `docker pull`/`push` traffic from every node in the
cluster and reads/writes the corresponding blobs from Azure Blob Storage.

## Architecture: Operator and Registry

Only the operator talks to Azure Resource Manager. The registry's Azure
calls are scoped almost entirely to the blob data plane of a single storage
account/container:

1. **Operator** (`cluster-image-registry-operator`) — runs as a
   **control plane** `Deployment` (on control-plane/master nodes for a
   standalone cluster, or in the management cluster's hosted-control-plane
   namespace for a HyperShift-hosted cluster). It implements
   `CreateStorage`/`RemoveStorage` for the Azure driver
   (`pkg/storage/azure/azure.go`) and does all of the ARM-facing work:
   `Microsoft.Storage/storageAccounts` create/read/delete, blob container
   create/read/delete via the management-plane API, `listKeys` (to retrieve
   the account key handed to the registry), resource tagging, and only if
   the user requests a private storage account, private endpoint/private
   DNS provisioning. This is the component that consumes the
   [Control Plane RBAC Permissions](#control-plane-rbac-permissions) below.
2. **Registry** (`registry` `Deployment`) — runs
   as a **data plane** workload, scheduled onto worker nodes via topology
   spread constraints (`node-role.kubernetes.io/worker`). It never calls
   ARM. It authenticates directly to the blob service
   (`https://<account>.blob.core.windows.net`) to `GET`/`PUT`/`DELETE` blobs
   for image layers and manifests, and to prune unreferenced blobs. This is
   the component that consumes the
   [Data Plane RBAC Permissions](#data-plane-rbac-permissions) below.

In short: the operator does the provisioning against Azure, the registry
only reads/writes the blobs inside the container the operator already
created.

## Control Plane RBAC Permissions

The operator is the only component that talks to Azure Resource Manager.
Every permission below comes from the `permissions` list in
[`manifests/01-registry-credentials-request-azure.yaml`](https://github.com/openshift/cluster-image-registry-operator/blob/main/manifests/01-registry-credentials-request-azure.yaml).

| # | Azure RBAC Permission (Action) | Semantic Meaning | Scope |
|---|---|---|---|
| 1 | `Microsoft.Storage/storageAccounts/read`<br>`Microsoft.Storage/storageAccounts/write`<br>`Microsoft.Storage/storageAccounts/delete` | Create, read, and delete the Azure Storage Account resource backing the registry (`assureStorageAccount`/`RemoveStorage`), and update its properties (e.g. disabling public network access when workload identity is used, or checking name availability). | Managed resource group |
| 2 | `Microsoft.Storage/storageAccounts/listKeys/action` | Retrieve the storage account's shared keys (`getAccountPrimaryKey`) so one can be handed to the registry pod as `REGISTRY_STORAGE_AZURE_ACCOUNTKEY`. | Managed resource group |
| 3 | `Microsoft.Storage/storageAccounts/blobServices/read` | Read the blob service properties of the storage account (e.g. soft-delete/versioning settings) as part of container management calls. | Managed resource group |
| 4 | `Microsoft.Storage/storageAccounts/blobServices/containers/read`<br>`Microsoft.Storage/storageAccounts/blobServices/containers/write`<br>`Microsoft.Storage/storageAccounts/blobServices/containers/delete` | Create, check for existence of, and delete the blob **container** backing the registry — all via the ARM control-plane API rather than the data plane (`assureContainerViaTrack2SDK`). | Managed resource group |
| 5 | `Microsoft.Storage/storageAccounts/blobServices/generateUserDelegationKey/action` | Obtain a user-delegation key from the storage account so a short-lived, Azure-AD-backed SAS can be minted for blob access/redirects when there is no account key (workload identity clusters). | Managed resource group |
| 6 | `Microsoft.Resources/tags/write` | Apply the cluster-ID tag (`kubernetes.io_cluster.<infra-id>: owned`) and any user-provided `Infrastructure.status.platformStatus.azure.resourceTags` to the created storage account. | Managed resource group |
| 7 | `Microsoft.Network/privateEndpoints/read`<br>`Microsoft.Network/privateEndpoints/write` | Create/read the private endpoint that connects the storage account to the cluster's VNet, only when a user requests a private storage account (`assurePrivateAccount`). | Managed resource group |
| 8 | `Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read`<br>`Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write` | Associate/read the private endpoint's private-DNS-zone-group so name resolution for the storage account resolves to its private IP. | Managed resource group |
| 9 | `Microsoft.Network/privateDnsZones/read`<br>`Microsoft.Network/privateDnsZones/write` | Read/create the private DNS zone used for the storage account's private-link name (`ConfigurePrivateDNS`). | Managed resource group (or the resource group hosting a shared/BYO Private DNS Zone) |
| 10 | `Microsoft.Network/privateDnsZones/join/action` | "Join" the private endpoint's NIC to the private DNS zone so the zone can be linked to it. | Managed resource group (or the resource group hosting a shared/BYO Private DNS Zone) |
| 11 | `Microsoft.Network/privateDnsZones/A/write` | Create the DNS `A` record pointing the storage account's private-link hostname at the private endpoint's private IP. | Managed resource group (or the resource group hosting a shared/BYO Private DNS Zone) |
| 12 | `Microsoft.Network/privateDnsZones/virtualNetworkLinks/read`<br>`Microsoft.Network/privateDnsZones/virtualNetworkLinks/write` | Create/read the link between the private DNS zone and the cluster's VNet, so in-cluster DNS resolves the zone. | Managed resource group (or the resource group hosting a shared/BYO Private DNS Zone) |
| 13 | `Microsoft.Network/networkInterfaces/read` | Read the NIC created for the private endpoint to retrieve its private IP for the DNS `A` record. | Managed resource group |
| 14 | `Microsoft.Storage/storageAccounts/PrivateEndpointConnectionsApproval/action` | Approve the private-endpoint connection request on the storage account so traffic through the endpoint is permitted. | Managed resource group |
| 15 | `Microsoft.Network/virtualNetworks/subnets/read`<br>`Microsoft.Network/virtualNetworks/subnets/join/action` | Discover the target subnet (by cluster tags, `GetSubnetsByVNet`) and attach a new private-endpoint NIC to it — "join" is required because Azure re-validates permission on every resource referenced when the private endpoint is written. | Resource group holding the cluster's (or a BYO) virtual network |
| 16 | `Microsoft.Network/virtualNetworks/join/action` | "Join" the VNet itself, required by ARM's validation of the VNet reference when creating the private endpoint. | Resource group holding the cluster's (or a BYO) virtual network |

> Permissions 7–16 (all `Microsoft.Network/...` and the
> `PrivateEndpointConnectionsApproval` action) are, per the manifest's own
> comment, "only necessary when users request the operator to configure a
> private storage account" (`spec.storage.azure.networkAccess.type: Internal`).
> They are not exercised on the default, publicly-accessible storage-account
> path.

## Data Plane RBAC Permissions

The registry pod never calls ARM. Every permission below comes from the
`dataPermissions` list in the same `CredentialsRequest` and is only
consulted when the registry is authenticating with an Azure AD identity
instead of the storage-account shared key (see next section).

| # | Azure RBAC Permission (Data Action) | Semantic Meaning | Scope |
|---|---|---|---|
| 1 | `Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read` | Download a blob (an image layer or manifest) — serves every `docker/podman pull`. | Blob container |
| 2 | `Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write`<br>`Microsoft.Storage/storageAccounts/blobServices/containers/blobs/add/action`<br>`Microsoft.Storage/storageAccounts/blobServices/containers/blobs/move/action` | Serve `docker/podman push`: upload/overwrite a full blob (`write`), append data during a chunked/resumable upload while a layer push is still in progress (`add/action`), and rename/commit a blob — e.g. moving it from its temporary upload path to its final digest-addressed path once the push completes (`move/action`). | Blob container |
| 3 | `Microsoft.Storage/storageAccounts/blobServices/containers/blobs/delete` | Delete a blob — used by the image pruner (`ImagePrunerController`) to garbage-collect layers no longer referenced by any image. | Blob container |

## Data Plane Kubernetes Service Accounts

| Service Account | Namespace | Component |
|---|---|---|
| `cluster-image-registry-operator` | `openshift-image-registry` | Operator (control plane) |
| `registry` | `openshift-image-registry` | Registry (data plane / operand) |

## See Also

- [`manifests/01-registry-credentials-request-azure.yaml`](https://github.com/openshift/cluster-image-registry-operator/blob/main/manifests/01-registry-credentials-request-azure.yaml) — the `CredentialsRequest` this document is derived from.

