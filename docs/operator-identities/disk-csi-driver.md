# Azure Disk CSI Driver Operator Identity

This operator has **two** distinct identities: a control plane identity used
by the CSI driver's controller running in the management cluster, and a
data plane identity federated to ServiceAccounts running on the user's
OpenShift cluster worker nodes.

## Why This Driver Is Needed

Kubernetes exposes storage to workloads through `PersistentVolumeClaim`
(PVC), `PersistentVolume` (PV), and `StorageClass` objects, but Kubernetes
itself has no built-in knowledge of how to talk to Azure. A `StorageClass`
names a `provisioner` for Azure Disks this is `disk.csi.azure.com` and
it is that provisioner's **CSI (Container Storage Interface) driver** that
actually does the work of turning a PVC into a real, attached, mountable
Azure Managed Disk. Without the Azure Disk CSI driver running in the
cluster, a `StorageClass` backed by `disk.csi.azure.com` has nothing to
fulfill it: PVCs referencing it would stay `Pending` forever, because no
component would ever call Azure to create and attach the disk.

## Architecture: Controller and Node Plugin

The upstream driver ships as two separate components. **The controller
does the Azure control-plane work**; the node plugin's job is almost
entirely local to the worker node:

1. **Controller** (`csi-azuredisk-controller`) — runs as a `Deployment`
   **in the control plane**. It implements the CSI
   *Controller Service* RPCs and does essentially all of the Azure-facing
   work: `CreateVolume`/`DeleteVolume` (provision/delete the underlying
   `Microsoft.Compute/disks` resource), `ControllerPublishVolume`/
   `ControllerUnpublishVolume` (attach/detach the disk to/from a VM or VMSS
   instance), `CreateSnapshot`/`DeleteSnapshot`, and
   `ControllerExpandVolume` (resize the underlying disk). This is the
   component that consumes the **control plane** identity's Azure RBAC
   permissions.
2. **Node plugin** (`csi-azuredisk-node`) — runs as a `DaemonSet` on every
   worker node, i.e. **in the data plane**. It implements the CSI *Node
   Service* RPCs: `NodeStageVolume`/`NodeUnstageVolume` (format the disk and
   mount it to a staging path on the node), `NodePublishVolume`/
   `NodeUnpublishVolume` (bind-mount it into the pod), and
   `NodeExpandVolume` (grow the filesystem after the controller has resized
   the disk). Most of this is local OS/filesystem work rather than Azure
   API calls. The node plugin normally learns which VM it is running on
   from the Instance Metadata Service (IMDS) and does not call Azure. When
   IMDS is disabled, it instead reads that VM or VMSS instance through
   Azure Resource Manager, which is the only case that needs the
   [Data Plane RBAC Permissions](#data-plane-rbac-permissions) below.

In short: the controller does the actual provisioning against Azure; the
node plugin only makes an already-provisioned disk usable by a pod on the
node it runs on.

## Control Plane RBAC Permissions

The controller does the Azure-facing provisioning work. It needs Azure
RBAC permissions so it can create, attach, detach, delete, snapshot, and
expand the underlying `Microsoft.Compute/disks` resources on the user's
behalf. Every permission below authorizes a piece of that Azure-facing work.

| # | Azure RBAC Permission (Action) | Semantic Meaning | Scope |
|---|---|---|---|
| 1 | `Microsoft.Compute/disks/read`<br>`Microsoft.Compute/disks/write`<br>`Microsoft.Compute/disks/delete` | Create, read, update, and delete the Azure Managed Disk resource backing a `PersistentVolume` (CSI `CreateVolume`/`DeleteVolume`). | Managed resource group |
| 2 | `Microsoft.Compute/disks/beginGetAccess/action` | Generate a read/write SAS URI for a disk's content, used for disk snapshot/clone and incremental-snapshot data access. | Managed resource group |
| 3 | `Microsoft.Compute/diskEncryptionSets/read` | Read a Disk Encryption Set so a disk can be created encrypted with the customer-managed key (CMK) referenced by that DES. | Managed resource group (or the resource group holding a customer-supplied DES) |
| 4 | `Microsoft.Compute/snapshots/read`<br>`Microsoft.Compute/snapshots/write`<br>`Microsoft.Compute/snapshots/delete` | Create, read, and delete disk snapshot resources for CSI `CreateSnapshot`/`DeleteSnapshot` (Kubernetes `VolumeSnapshot` support). | Managed resource group |
| 5 | `Microsoft.Compute/virtualMachines/read`<br>`Microsoft.Compute/virtualMachines/write` | Read a node's VM model and update it to attach/detach a Managed Disk in its `storageProfile.dataDisks` (CSI `ControllerPublishVolume`/`ControllerUnpublishVolume`). | Managed resource group |
| 6 | `Microsoft.Compute/locations/operations/read`<br>`Microsoft.Compute/locations/DiskOperations/read` | Poll the status/result of long-running Compute operations (e.g. an in-progress disk create/attach/detach), and read per-region disk operation metadata. | Subscription (region-scoped provider operation, not tied to a specific resource) |
| 7 | `Microsoft.Resources/subscriptions/resourceGroups/read`<br>`Microsoft.Resources/subscriptions/resourceGroups/*/read` | Read the resource group's own properties, and generically read any resource type inside it that isn't covered by a more specific permission above. | Managed resource group |
| 8 | `Microsoft.Network/applicationGateways/backendAddressPools/join/action`<br>`Microsoft.Network/applicationSecurityGroups/joinIpConfiguration/action`<br>`Microsoft.Network/loadBalancers/backendAddressPools/join/action`<br>`Microsoft.Network/loadBalancers/inboundNatPools/join/action`<br>`Microsoft.Network/loadBalancers/probes/join/action`<br>`Microsoft.Network/networkInterfaces/join/action`<br>`Microsoft.Network/networkSecurityGroups/join/action`<br>`Microsoft.Network/publicIPPrefixes/join/action`<br>`Microsoft.Network/virtualNetworks/subnets/join/action` | Not used by the disk driver's own logic — these are "join" permissions on network resources that may be *referenced* by a node's VM/VMSS model (its NIC, NSG, LB backend pool/NAT pool/probe, App Gateway backend pool, subnet, etc.). Azure re-validates permission on every resource referenced in a VM/VMSS body on any `write`, so the controller needs `join` rights on all of them or its disk-attach `PUT`/`PATCH` to that VM/VMSS is rejected — only actually required if the node's VM/VMSS happens to have that additional resource configured. | Managed resource group (subnet joins may also require this on the resource group holding a customer-supplied/BYO virtual network) |
| 9 | `Microsoft.KeyVault/vaults/deploy/action` | Allow an ARM template deployment to reference a Key Vault secret/certificate, needed only if the node's VM/VMSS deployment pulls a value from Key Vault (e.g. a VM extension). | Resource group holding the referenced Key Vault |
| 10 | `Microsoft.ManagedIdentity/userAssignedIdentities/assign/action` | Assign a user-assigned managed identity to a VM/VMSS, needed only if the node's VM/VMSS has an additional user-assigned identity attached. | Managed resource group |

> Source: [`cluster-storage-operator/manifests/03_credentials_request_azure.yaml`](https://github.com/openshift/cluster-storage-operator/blob/main/manifests/03_credentials_request_azure.yaml) —
> the upstream `CredentialsRequest` the disk CSI driver operator submits to
> the OpenShift Cloud Credential Operator; this is the authoritative list of
> permissions the operator actually requests in-cluster. Some RBAC permissions
> needed only when the node's VM or VMSS has the corresponding additional resource
> configured.
>
> Cross-reference: the DEV-environment approximation of this role's actions
> in [`dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`](../../dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam)
> ("Azure Red Hat OpenShift Disk Storage Operator - Dev") covers a subset of
> this list, confirm against the production built-in role before finalizing
> this table.

## Data Plane RBAC Permissions

The node plugin does not provision or attach disks. By the time it runs,
the controller has already created the disk and attached it to the node's
VM using the control plane identity. The node plugin formats that disk,
mounts it to a staging path, and bind-mounts it into the pod.

It normally discovers which VM it is running on from the Azure Instance
Metadata Service (IMDS, `169.254.169.254`) and makes no Azure Resource
Manager calls. **No data plane Azure RBAC permissions are required when
IMDS is available.**

When IMDS is disabled, the node plugin cannot read that local metadata and
must look the instance up through Azure Resource Manager. Only in that
case does the data plane identity need the permissions below.

| # | Azure RBAC Permission (Action) | Semantic Meaning | Scope |
|---|---|---|---|
| 1 | `Microsoft.Compute/virtualMachines/read` | Read the standalone virtual machine the node is running on, so the node plugin can identify the instance and the data disks attached to it. | Managed resource group |
| 2 | `Microsoft.Compute/virtualMachineScaleSets/virtualMachines/read` | Read the VMSS virtual machine instance the node is running on, so the node plugin can identify the instance and the data disks attached to it. | Managed resource group |

> These two permissions are optional. They are required only when IMDS is
> disabled. With IMDS enabled, the node plugin reads instance metadata
> locally and does not call Azure.

## Data Plane Kubernetes Service Accounts

The data plane identity is federated (via OIDC) to the following
ServiceAccounts, so that pods running as these ServiceAccounts can obtain
Azure AD tokens for the data plane identity:

| Service Account | Namespace |
|---|---|
| `azure-disk-csi-driver-operator` | `openshift-cluster-csi-drivers` |
| `azure-disk-csi-driver-controller-sa` | `openshift-cluster-csi-drivers` |

## See Also

- [Operator Identities Index](README.md)
- [`internal/azure/cluster_scoped_identities_config.go`](../../internal/azure/cluster_scoped_identities_config.go)
- [kubernetes-sigs/azuredisk-csi-driver](https://github.com/kubernetes-sigs/azuredisk-csi-driver) — upstream Azure Disk CSI driver source and docs
