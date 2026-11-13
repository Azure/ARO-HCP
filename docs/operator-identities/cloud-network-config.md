# Cloud Network Config Controller Identity

This operator has a **single** identity: a control plane identity used by
the controller running in the management cluster. It does not have a data
plane identity.

## Why This Operator Is Needed

By default, when a pod's traffic leaves the cluster it gets SNAT'd to
*some* node IP — which node depends on scheduling, so the source IP a
pod's egress traffic shows up with is unpredictable. Some external
services only allow traffic from an allowlisted IP, which doesn't work if
that IP can change any time a pod is rescheduled.

**EgressIP** is the OVN-Kubernetes feature that solves this: a cluster
admin assigns one or more fixed, reserved private IPs to a namespace (or a
pod selector), and OVN-Kubernetes pins that namespace's outbound traffic to
one of those IPs instead of a random node IP. EgressIP itself is
cloud-agnostic — OVN-Kubernetes decides *which* node should host each
EgressIP and expresses that decision as a `CloudPrivateIPConfig` custom
resource.

`cloud-network-config-controller` is the component that turns that
cloud-agnostic decision into something that actually works on Azure: it
watches `CloudPrivateIPConfig` objects and calls the Azure API to add or
remove that IP as a secondary private IP configuration on the right
worker node's NIC. Without this controller, `CloudPrivateIPConfig` objects
would never reconcile — EgressIP would stay a Kubernetes-level intent with
no effect on the actual Azure network.

## Architecture: What This Identity Actually Does

The controller is a single binary with two sub-controllers, both of which
use this identity:

1. **`CloudPrivateIPConfigController`** — watches the cluster-scoped
   `CloudPrivateIPConfig` custom resource. Its `spec.node` is the node
   OVN-Kubernetes *wants* to host the egress IP; its `status.node` is the
   node it's *actually* assigned to today. When the two disagree, the
   controller calls Azure to **assign** the IP as a secondary private IP
   configuration on the desired node's NIC, **release** it from a node
   that no longer wants it, or do a release-then-assign to move it from
   one node to another. On startup it also runs a one-time cleanup pass
   against the public load balancer — see permission 5 below.
2. **`NodeController`** — watches `Node` objects and writes the
   `cloud.network.openshift.io/egress-ipconfig` annotation on each one,
   containing that node's subnet CIDR, how many more secondary IPs its NIC
   can still take on, and which NIC/subnet to use. OVN-Kubernetes reads
   this annotation to decide which nodes are even eligible to host a given
   EgressIP before `CloudPrivateIPConfig` objects are created.

In ARO HCP, this controller's pod runs on the **management cluster**
rather than inside the guest cluster itself.

## Control Plane RBAC Permissions

The controller needs Azure RBAC permissions so it can resolve a worker
node to its underlying VM and NIC, read the cluster's VNet to pick/validate
IPs and subnets, and add or remove a secondary private IP configuration on
a node's NIC on OVN-Kubernetes's behalf.

| # | Azure RBAC Permission (Action) | Semantic Meaning | Scope |
|---|---|---|---|
| 1 | `Microsoft.Compute/virtualMachines/read` | Read the Azure VM backing a worker node, to resolve which network interface(s) belong to it before reading or updating them. | Managed resource group |
| 2 | `Microsoft.Network/networkInterfaces/read`<br>`Microsoft.Network/networkInterfaces/write` | Read a worker node's NIC to see its current IP configurations, then update the NIC to add a new secondary private IP configuration (assigning an EgressIP to that node) or remove one (releasing it). | Managed resource group |
| 3 | `Microsoft.Network/virtualNetworks/read` | Read the cluster's VNet address space to compute the per-node subnet info written to the `cloud.network.openshift.io/egress-ipconfig` node annotation that OVN-Kubernetes relies on for placement decisions. | Customer's cluster VNet |
| 4 | `Microsoft.Network/virtualNetworks/subnets/join/action` | Satisfy Azure's linked-authorization check whenever a NIC IP configuration references a subnet outside the controller's directly-scoped resource group — required in particular for customer-supplied (BYO) VNets/subnets that live outside the managed resource group. | Customer's cluster subnet |
| 5 | `Microsoft.Network/loadBalancers/backendAddressPools/read`<br>`Microsoft.Network/loadBalancers/backendAddressPools/join/action` | Historical/compatibility cleanup, not part of normal EgressIP operation: on startup, the controller checks the public load balancer's backend address pool and removes any EgressIP a previous version of the controller may have added to it. Current versions never add EgressIPs to a load balancer backend pool — this only runs to clean up state left behind by older controller versions during an upgrade. | Managed resource group |

> Source: [`manifests/02-cncc-credentials.yaml`](https://github.com/openshift/cluster-network-operator/blob/master/manifests/02-cncc-credentials.yaml)
> (the `openshift-cloud-network-config-controller-azure` `CredentialsRequest`
> block) — the upstream `CredentialsRequest` the cloud-network-config
> controller submits to the OpenShift Cloud Credential Operator; this is the
> authoritative list of permissions the operator actually requests
> in-cluster.
>
> Cross-reference: the DEV-environment approximation of this role's actions
> in [`dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`](../../dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam)
> ("Azure Red Hat OpenShift Network Operator - Dev") matches this list
> exactly, and the production built-in role (`Azure Red Hat OpenShift
> Network Operator`) grants the
> same set of actions.

## See Also

- [Operator Identities Index](README.md)
- [`internal/azure/cluster_scoped_identities_config.go`](../../internal/azure/cluster_scoped_identities_config.go)
- [`manifests/02-cncc-credentials.yaml`](https://github.com/openshift/cluster-network-operator/blob/master/manifests/02-cncc-credentials.yaml) — upstream `CredentialsRequest` for the cloud-network-config controller
- [openshift/cluster-network-operator](https://github.com/openshift/cluster-network-operator) — hosts the controller's manifests
- [openshift/cloud-network-config-controller](https://github.com/openshift/cloud-network-config-controller) — upstream controller source, including the Azure cloud provider implementation
