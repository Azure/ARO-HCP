# Cloud Controller Manager Identity

This operator has a **single** identity: a control plane identity used by
the Cloud Controller Manager running in the management cluster. It does not
have a data plane identity.

## Why This Operator Is Needed

Kubernetes has no built-in knowledge of Azure load balancers, public IPs,
network security groups, or how to map a `Node` object to its underlying
Azure VM. The **Cloud Controller Manager** (CCM) is the component that
bridges Kubernetes objects with Azure infrastructure — without it,
`Service type: LoadBalancer` objects would never get an external IP, nodes
would never be initialized with their Azure metadata, and cloud-aware
networking features would not function.

## Architecture: What This Identity Actually Does

The Azure CCM is a single binary that runs three sub-controllers, all of
which use this identity:

1. **Service Controller** — watches `Service` objects of type `LoadBalancer`
   and reconciles them into Azure Load Balancers. This includes creating,
   updating, and deleting load balancers, their frontend IP configurations
   (public IPs), backend address pools, health probes, load balancing
   rules, inbound NAT rules, and the corresponding NSG rules to allow
   traffic. It also manages Private Link Services when a Service is
   annotated for internal load balancing with private link. After
   reconciliation, it updates `Service.status.loadBalancer` with the
   assigned IP so Kubernetes knows where to route traffic.

2. **Node Controller** — initializes `Node` objects with their Azure
   metadata: provider ID (`azure:///...`), cloud labels (region, zone,
   instance type), node addresses, and removes the
   `node.cloudprovider.kubernetes.io/uninitialized` taint. It also
   monitors node health by checking the Azure API — if a VM no longer
   exists in Azure (deleted), the controller removes the corresponding
   `Node` object from Kubernetes.

3. **Route Controller** — configures Azure route tables for pod-to-pod
   networking across nodes. This controller is typically **disabled** in
   OVN-Kubernetes environments (like ARO HCP) because OVN provides
   routing for a separate pod-CIDR overlay. Subnet and linked-resource
   permissions can still be required even when cloud routes are disabled.

In ARO HCP, the CCM pod runs on the **management cluster** as part of
the Hosted Control Plane, not inside the guest cluster itself.

## Control Plane RBAC Permissions

The CCM needs Azure RBAC permissions to manage load balancers and their
associated resources (public IPs, NSG rules, backend pools), to read VMs
and NICs for node initialization and health checks, and to join subnets
for cross-resource-group authorization.

| # | Azure RBAC Permission (Action) | Semantic Meaning | Scope |
|---|---|---|---|
| 1 | `Microsoft.Compute/virtualMachines/read` | Read the Azure VM backing a worker node to retrieve its provider ID, addresses, instance type, and zone for Node initialization; also used to verify node health. | Managed resource group |
| 2 | `Microsoft.Network/loadBalancers/read`<br>`Microsoft.Network/loadBalancers/write`<br>`Microsoft.Network/loadBalancers/delete` | Create, read, update, and delete Azure Load Balancers when reconciling `Service type: LoadBalancer` objects. The Service controller manages the full LB lifecycle, including cleanup when a Service is deleted. | Managed resource group |
| 3 | `Microsoft.Network/loadBalancers/backendAddressPools/join/action` | Add a node's NIC to a load balancer's backend address pool so the node can receive traffic for a LoadBalancer Service. | Managed resource group |
| 4 | `Microsoft.Network/loadBalancers/inboundNatRules/join/action` | Associate inbound NAT rules with a NIC for direct port-forwarding to a specific backend node through the load balancer. | Managed resource group |
| 5 | `Microsoft.Network/loadBalancers/loadBalancingRules/read` | Read the load balancing rules on a load balancer to determine its current configuration during reconciliation. | Managed resource group |
| 6 | `Microsoft.Network/publicIPAddresses/read`<br>`Microsoft.Network/publicIPAddresses/write`<br>`Microsoft.Network/publicIPAddresses/delete` | Create, read, and delete public IP addresses used as frontend IPs for LoadBalancer Services. Each externally-reachable Service gets its own public IP. | Managed resource group |
| 7 | `Microsoft.Network/publicIPAddresses/join/action` | Associate a public IP address with a load balancer's frontend IP configuration. | Managed resource group |
| 8 | `Microsoft.Network/publicIPPrefixes/join/action` | Allocate a public IP address from a public IP prefix for a LoadBalancer Service, for example via the `service.beta.kubernetes.io/azure-pip-prefix-id` annotation. The resulting public IP resource is used by the load balancer's frontend. | Managed resource group |
| 9 | `Microsoft.Network/networkInterfaces/read`<br>`Microsoft.Network/networkInterfaces/write` | Read a node's NIC to discover its IP addresses and subnet, and update it to join or leave a load balancer backend pool or update its NSG association. | Managed resource group |
| 10 | `Microsoft.Network/networkSecurityGroups/read`<br>`Microsoft.Network/networkSecurityGroups/write` | Read and update network security groups to add or remove rules that allow load balancer health probe traffic and service-port traffic to reach backend nodes. | Resource group holding the cluster's (or a BYO) virtual network/subnet |
| 11 | `Microsoft.Network/networkSecurityGroups/join/action` | Associate an NSG with a NIC or subnet when the CCM needs to attach security rules to a network resource. | Resource group holding the cluster's (or a BYO) virtual network/subnet |
| 12 | `Microsoft.Network/virtualNetworks/subnets/read`<br>`Microsoft.Network/virtualNetworks/subnets/join/action`<br>`Microsoft.Network/virtualNetworks/subnets/write` | Read the customer-supplied subnet for node network configuration, satisfy Azure's cross-resource-group linked-authorization check for BYO VNets, and write subnet updates when attaching route tables or NSG associations. | Resource group holding the cluster's (or a BYO) virtual network/subnet |
| 13 | `Microsoft.Network/privatelinkservices/read`<br>`Microsoft.Network/privatelinkservices/write`<br>`Microsoft.Network/privatelinkservices/delete` | Create, read, and delete Private Link Services for internal LoadBalancer Services that expose endpoints via Azure Private Link, enabling private connectivity from other VNets or subscriptions. | Managed resource group |
| 14 | `Microsoft.Network/privatelinkservices/privateEndpointConnections/delete` | Delete private endpoint connections on a Private Link Service during cleanup, ensuring stale connections are removed when a Service or its PLS is deleted. | Managed resource group |
| 15 | `Microsoft.Network/routeTables/join/action` | Satisfy Azure's linked-authorization check when updating a subnet that references a route table. Required even when the Route controller is disabled. | Resource group holding the cluster's (or a BYO) virtual network/subnet |
| 16 | `Microsoft.Network/natGateways/join/action` | Satisfy Azure's linked-authorization check when a subnet references a NAT gateway for outbound connectivity, ensuring the CCM can update subnet properties without losing the NAT gateway association. | Resource group holding the cluster's (or a BYO) virtual network/subnet |
| 17 | `Microsoft.Network/networkIntentPolicies/join/action`<br>`Microsoft.Network/serviceEndpointPolicies/join/action` | Satisfy Azure's linked-authorization checks when a subnet has network intent policies or service endpoint policies attached — required so the CCM can update the subnet without Azure rejecting the PUT for missing join permissions on referenced child resources. | Resource group holding the cluster's (or a BYO) virtual network/subnet |
| 18 | `Microsoft.Network/networkManagers/ipamPools/associateResourcesToPool/action` | Associate VNet resources with an Azure Network Manager IPAM pool, used when the cluster's networking is managed through Azure IPAM. | Resource group holding the cluster's (or a BYO) virtual network/subnet |
| 19 | `Microsoft.Network/applicationSecurityGroups/joinNetworkSecurityRule/action` | Reference application security groups in NSG rules, allowing the CCM to create security rules that target ASG-grouped resources rather than raw IP ranges. | Resource group holding the cluster's (or a BYO) virtual network/subnet |

> Source:
> [`manifests/0000_26_cloud-controller-manager-operator_14_credentialsrequest-azure.yaml`](https://github.com/openshift/cluster-cloud-controller-manager-operator/blob/main/manifests/0000_26_cloud-controller-manager-operator_14_credentialsrequest-azure.yaml)
> (the `openshift-azure-cloud-controller-manager` `CredentialsRequest`
> block) — the upstream `CredentialsRequest` the CCM operator submits to
> the OpenShift Cloud Credential Operator; this is the authoritative list
> of permissions the operator actually requests in-cluster.
>
> Cross-reference: the production built-in role (`Azure Red Hat OpenShift
> Cloud Controller Manager`, `a1f96423-95ce-4224-ab27-4e3dc72facd4`)
> grants the same set of 30 actions. The DEV-environment approximation in
> [`dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`](../../dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam)
> ("Azure Red Hat OpenShift Cloud Controller Manager - Dev") contains 18
> of these 30 actions — a subset that covers the core LB, IP, NIC, NSG,
> and subnet operations but omits the newer Private Link Service, IPAM,
> NAT gateway, and route table permissions.

## See Also

- [Operator Identities Index](README.md)
- [`internal/azure/cluster_scoped_identities_config.go`](../../internal/azure/cluster_scoped_identities_config.go)
- [`manifests/0000_26_cloud-controller-manager-operator_14_credentialsrequest-azure.yaml`](https://github.com/openshift/cluster-cloud-controller-manager-operator/blob/main/manifests/0000_26_cloud-controller-manager-operator_14_credentialsrequest-azure.yaml) — upstream `CredentialsRequest` for the CCM
- [openshift/cluster-cloud-controller-manager-operator](https://github.com/openshift/cluster-cloud-controller-manager-operator) — upstream operator that manages the CCM deployment
- [kubernetes-sigs/cloud-provider-azure](https://github.com/kubernetes-sigs/cloud-provider-azure) — the Azure cloud provider implementation (the actual CCM binary)
