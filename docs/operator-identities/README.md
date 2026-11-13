# Operator Managed Identities

Every ARO HCP cluster is provisioned with a set of Azure user-assigned managed
identities ("operator identities") that the OpenShift cluster operators use to
call Azure APIs on the customer's behalf. Each operator identity is granted an
Azure RBAC Role Definition, scoped to what that specific operator needs to do
its job.

This directory documents each operator identity: what Azure RBAC permissions
it holds, what each permission semantically means, and the scope at which it
is needed.

## Control Plane vs. Data Plane Identities

Operator identities are split into two "planes":

- **Control plane identities** are used by the operator's control-plane
  component (running in the management cluster, alongside the rest of the
  Hosted Control Plane) to reconcile Azure infrastructure — for example,
  creating a load balancer or a managed disk.
- **Data plane identities** are used by workloads running inside the
  customer's OpenShift cluster itself (the "data plane"), federated to a
  specific Kubernetes ServiceAccount via workload identity federation
  (OIDC). These identities let in-cluster components call Azure APIs directly.

Most operators only need a control plane identity. **Three operators need
both** a control plane identity and a separate data plane identity:

- [Disk CSI Driver](disk-csi-driver.md)
- [File CSI Driver](file-csi-driver.md)
- [Image Registry](image-registry.md)

## All Operator Identities

| Operator identifier | Operator | Plane(s) | Requirement | Doc |
|---|---|---|---|---|
| `cluster-api-azure` | Cluster API Provider Azure (CAPZ) | Control plane | Always |  |
| `control-plane` | Hypershift Control Plane Operator | Control plane | Always |  |
| `cloud-controller-manager` | Cloud Controller Manager | Control plane | Always |  |
| `ingress` | Cluster Ingress Operator | Control plane | Always |  |
| `disk-csi-driver` | Azure Disk CSI Driver Operator | Control plane + Data plane | Always | [disk-csi-driver.md](disk-csi-driver.md) |
| `file-csi-driver` | Azure File CSI Driver Operator | Control plane + Data plane | Always | [file-csi-driver.md](file-csi-driver.md) |
| `image-registry` | Cluster Image Registry Operator | Control plane + Data plane | Always | [image-registry.md](image-registry.md) |
| `cloud-network-config` | Cloud Network Config Controller (Network Operator) | Control plane | Always | |
| `kms` | KMS Plugin (etcd encryption) | Control plane | OnEnablement |  |

`Requirement` reflects whether the identity is always assigned to a cluster
(`Always`) or only assigned when a particular feature is enabled
(`OnEnablement`, e.g. `kms` is only needed when etcd data encryption with a
customer-managed key is enabled).

Not documented here: the `service` managed identity. It is the Resource
Provider / Clusters Service's own managed identity rather than an identity
tied to a specific OpenShift cluster operator.

## Where To Look

- [`internal/azure/cluster_scoped_identities_config.go`](../../internal/azure/cluster_scoped_identities_config.go) —
  canonical, code-level definition of every operator identity, its plane(s),
  its Azure Role Definition(s), and (for data plane identities) the
  Kubernetes ServiceAccounts it federates to.
- [`dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`](../../dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam) —
  DEV-environment custom-role approximation of each operator's Azure RBAC
  `actions`/`dataActions`. Useful as a starting reference when filling in the
  permission tables, but should be cross-checked against the real production
  built-in Role Definition before being treated as authoritative.
- [`api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/examples/2025-12-23-preview/HcpOperatorIdentityRoleSets_Get_MaximumSet_Gen.json`](../../api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/examples/2025-12-23-preview/HcpOperatorIdentityRoleSets_Get_MaximumSet_Gen.json) —
  example of the customer-facing ARM API payload that lists, per OpenShift
  version, which operator identities and Role Definitions are required.
- [`backend/pkg/controllers/cluster/roleassignments/role_assignments_controller.go`](../../backend/pkg/controllers/cluster/roleassignments/role_assignments_controller.go) —
  backend controller that creates the actual Azure role assignments for each
  operator identity (today, scoped to the cluster's managed resource group).
