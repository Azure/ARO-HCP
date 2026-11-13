# Azure File CSI Driver Operator Identity

This operator has **two** distinct identities: a control plane identity used
by the CSI driver's controller running in the management cluster, and a
data plane identity federated to ServiceAccounts running on the user's
OpenShift cluster worker nodes.

## Why This Driver Is Needed

Kubernetes exposes storage to workloads through `PersistentVolumeClaim`
(PVC), `PersistentVolume` (PV), and `StorageClass` objects, but Kubernetes
itself has no built-in knowledge of how to talk to Azure. A `StorageClass`
names a `provisioner` for Azure Files this is `file.csi.azure.com` and
it is that provisioner's **CSI (Container Storage Interface) driver** that
actually does the work of turning a PVC into a real, mountable Azure Files
share. Without the Azure File CSI driver running in the cluster, a
`StorageClass` backed by `file.csi.azure.com` has nothing to fulfill it:
PVCs referencing it would stay `Pending` forever, because no component
would ever call Azure to create the Storage Account/share and no node
component would know how to mount it.

## Architecture: Controller and Node Plugin

The upstream driver ships as two separate components, and just like the
disk CSI driver **only the controller talks to the Azure control
plane**:

1. **Controller** (`csi-azurefile-controller`) — runs as a `Deployment`
   **in the control plane**. It implements the CSI
   *Controller Service* RPCs and does essentially all of the Azure-facing
   work: `CreateVolume`/`DeleteVolume` (create/delete the Storage Account,
   if needed, and the Azure Files share inside it), `CreateSnapshot`/
   `DeleteSnapshot` (share snapshots), and `ControllerExpandVolume` (grow
   the share's quota). This is the component that consumes the **control
   plane** identity's Azure RBAC permissions. Unlike the disk driver, there
   is no `ControllerPublishVolume`/`ControllerUnpublishVolume` "attach to a
   VM" step, an Azure Files share is a network share, not a block device,
   so there is nothing to attach or detach at the VM level.
2. **Node plugin** (`csi-azurefile-node`) — runs as a `DaemonSet` on every
   worker node, i.e. **in the data plane**. It implements the CSI *Node
   Service* RPCs: `NodeStageVolume`/`NodeUnstageVolume` (mount the share
   over SMB or NFS to a staging path on the node, using the storage
   account key/SAS the controller placed in a Kubernetes `Secret`, or a
   workload-identity token), `NodePublishVolume`/`NodeUnpublishVolume`
   (bind-mount it into the pod), and `NodeExpandVolume`. This is local
   mount/network-filesystem work rather than an Azure Resource Manager API
   call, which is why it is the **data plane** identity, federated to the
   node plugin's ServiceAccount, that is scoped so narrowly.

In short: the controller does the actual provisioning against Azure (the
Storage Account and the file share); the node plugin only mounts an
already-provisioned share and makes it available to the pod on the node it
runs on.

## Control Plane RBAC Permissions

The controller is the component that talks to Azure. It needs Azure RBAC
permissions so it can create and manage the Storage Account and file
shares backing Azure Files `PersistentVolume`s, and if the storage
account is created with a private endpoint, wire up the private
networking (subnet, private endpoint, and private DNS zone) around it.

| # | Azure RBAC Permission (Action) | Semantic Meaning | Scope |
|---|---|---|---|
| 1 | `Microsoft.Storage/storageAccounts/read`<br>`Microsoft.Storage/storageAccounts/write`<br>`Microsoft.Storage/storageAccounts/listKeys/action` | Create, read, and update the Storage Account that hosts the Azure Files shares, and retrieve its access keys (used to mount shares and build connection strings). | Managed resource group |
| 2 | `Microsoft.Storage/operations/read` | Poll the status/result of a long-running Storage Account operation. | Subscription (region-scoped provider operation, not tied to a specific resource) |
| 3 | `Microsoft.Storage/storageAccounts/fileServices/read`<br>`Microsoft.Storage/storageAccounts/fileServices/shares/read`<br>`Microsoft.Storage/storageAccounts/fileServices/shares/write`<br>`Microsoft.Storage/storageAccounts/fileServices/shares/delete` | Read file-service settings on the Storage Account, and create/read/delete the actual Azure Files share — the real target of CSI `CreateVolume`/`DeleteVolume` for this driver. | Managed resource group |
| 4 | `Microsoft.Network/virtualNetworks/join/action`<br>`Microsoft.Network/virtualNetworks/subnets/join/action`<br>`Microsoft.Network/virtualNetworks/subnets/write`<br>`Microsoft.Network/virtualNetworks/subnets/read` | Join and, if needed, create a subnet to host the storage account's private endpoint NIC. Only needed when the storage account is created with a private endpoint. | Resource group holding the cluster's (or a BYO) virtual network |
| 5 | `Microsoft.Network/privateEndpoints/write`<br>`Microsoft.Network/privateEndpoints/read` | Create and read the Private Endpoint resource that connects the Storage Account privately into the VNet. Only needed for private-endpoint storage accounts. | Managed resource group |
| 6 | `Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read`<br>`Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write` | Manage the DNS zone group linking the private endpoint to its Private DNS Zone, so the storage account gets an automatic internal DNS record. Only needed for private-endpoint storage accounts. | Managed resource group |
| 7 | `Microsoft.Network/privateDnsZones/join/action`<br>`Microsoft.Network/privateDnsZones/write`<br>`Microsoft.Network/privateDnsZones/read`<br>`Microsoft.Network/privateDnsZones/virtualNetworkLinks/write`<br>`Microsoft.Network/privateDnsZones/virtualNetworkLinks/read` | Create/join the Private DNS Zone used for the storage account's private endpoint and link it to the cluster's VNet, so the private hostname resolves internally. Only needed for private-endpoint storage accounts. | Managed resource group (or the resource group hosting a shared/BYO Private DNS Zone) |
| 8 | `Microsoft.Network/privateDnsOperationStatuses/read`<br>`Microsoft.Network/locations/operations/read` | Poll the status/result of long-running private DNS and network operations. Only needed for private-endpoint storage accounts. | Subscription (region-scoped provider operation) |
| 9 | `Microsoft.Storage/storageAccounts/PrivateEndpointConnectionsApproval/action` | Approve the private endpoint connection request on the Storage Account side, completing the private-link handshake. Only needed for private-endpoint storage accounts. | Managed resource group |
| 10 | `Microsoft.Network/serviceEndpointPolicies/join/action`<br>`Microsoft.Network/natGateways/join/action`<br>`Microsoft.Network/networkIntentPolicies/join/action`<br>`Microsoft.Network/networkSecurityGroups/join/action`<br>`Microsoft.Network/routeTables/join/action`<br>`Microsoft.Network/networkManagers/ipamPools/associateResourcesToPool/action` | Not used by the file driver's own logic — "join" permissions on other resources that may be attached to the subnet it writes to (row 4): a Service Endpoint Policy, NAT Gateway, Network Intent Policy, NSG, Route Table, or IPAM pool. Azure re-validates permission on every resource referenced by a subnet on write, so these are only needed if that subnet happens to have one of them attached. | Resource group holding the cluster's (or a BYO) virtual network/subnet |

> Source: [`cluster-storage-operator/manifests/03_credentials_request_azure_file.yaml`](https://github.com/openshift/cluster-storage-operator/blob/main/manifests/03_credentials_request_azure_file.yaml) —
> the upstream `CredentialsRequest` the file CSI driver operator submits to
> the OpenShift Cloud Credential Operator; this is the authoritative list of
> permissions the operator actually requests in-cluster.
>
> Cross-reference: the DEV-environment approximation of this role's actions
> in [`dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`](../../dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam)
> ("Azure Red Hat OpenShift File Storage Operator - Dev") covers a subset of
> this list; confirm against the production built-in role before finalizing
> this table.

## Data Plane Kubernetes Service Accounts

The data plane identity is federated (via OIDC) to the following
ServiceAccounts, so that pods running as these ServiceAccounts can obtain
Azure AD tokens for the data plane identity:

| Service Account | Namespace |
|---|---|
| `azure-file-csi-driver-operator` | `openshift-cluster-csi-drivers` |
| `azure-file-csi-driver-controller-sa` | `openshift-cluster-csi-drivers` |
| `azure-file-csi-driver-node-sa` | `openshift-cluster-csi-drivers` |

## See Also

- [Operator Identities Index](README.md)
- [`internal/azure/cluster_scoped_identities_config.go`](../../internal/azure/cluster_scoped_identities_config.go)
- [`cluster-storage-operator/manifests/03_credentials_request_azure_file.yaml`](https://github.com/openshift/cluster-storage-operator/blob/main/manifests/03_credentials_request_azure_file.yaml) — upstream `CredentialsRequest` for the file CSI driver operator
- [kubernetes-sigs/azurefile-csi-driver](https://github.com/kubernetes-sigs/azurefile-csi-driver) — upstream Azure File CSI driver source and docs
