# Cluster Ingress Operator Identity

This operator has a **single** identity: a control plane identity used by
the operator's controller running in the management cluster. It does not
have a data plane identity.

## Why This Operator Is Needed

`cluster-ingress-operator` is the OpenShift operator that manages the
cluster's **router** — the HAProxy-based ingress controller that routes
external traffic to in-cluster Services as specified by `Route` and
`Ingress` objects. Every OpenShift cluster, including the OpenShift-based
guest clusters ARO HCP provisions, needs a running router for
`Route`/`Ingress`-exposed Services to be reachable from outside the
cluster. Without this operator, nothing reconciles the router `Deployment`
or keeps its `IngressController` configuration applied.

## Architecture: What This Identity Actually Does

ARO HCP uses a multi-layer ingress architecture. The ingress operator
identity belongs to **Layer 3** (the hosted cluster level):

1. **Layer 1 — Service Cluster (Istio Ingress Gateway):** An Istio ingress
   gateway in the `aks-istio-ingress` namespace handles incoming traffic for
   ARO HCP platform services (RP frontend, etc.) and performs TLS
   termination using certificates from Azure Key Vault. No per-cluster
   managed identity is provisioned for this layer.
2. **Layer 2 — Management Cluster (Shared Ingress):** A shared ingress
   controller in the `hypershift-sharedingress` namespace uses SNI-based TLS
   passthrough to route traffic to the correct hosted control plane. It runs
   as a `Service` of type `LoadBalancer` with an auto-assigned public IP.
   No per-cluster managed identity is provisioned for this layer.
3. **Layer 3 — Hosted Cluster (OpenShift Ingress Operator):** The standard
   OpenShift ingress operator runs inside the hosted cluster's control
   plane, managing the `IngressController` (the HAProxy router). It creates
   DNS records pointing to the load balancer IP for application routes
   (`*.apps`). **This is the layer this identity belongs to.**

Only Layer 3 uses a per-cluster Azure managed identity for DNS — it is the
only layer whose DNS records are managed via this operator identity.

## Control Plane RBAC Permissions


| #   | Azure RBAC Permission (Action)                                                                           | Semantic Meaning                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | Scope                                                           |
| --- | -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| 1   | `Microsoft.Network/dnsZones/A/write` `Microsoft.Network/dnsZones/A/delete`                               | Create/update or delete the wildcard `*.apps` A record set within the cluster's public DNS subzone, pointing application routes to the router's load balancer IP. The upstream operator's `dns` sub-controller reconciles a `DNSRecord` custom resource by calling an Azure DNS provider client to write this record.                                                                                                                                                              | Managed resource group (the cluster's delegated CX DNS subzone) |
| 2   | `Microsoft.Network/privateDnsZones/A/write` `Microsoft.Network/privateDnsZones/A/delete`                 | Create/update or delete the wildcard `*.apps` A record set within the cluster's private DNS zone, pointing application routes to the router's load balancer private IP. Used when the cluster's ingress is only reachable privately (private clusters / a `Private`-scoped `IngressController`).                                                                                                                                                                                   | Managed resource group (private DNS zone equivalent)            |
| 3   | `Microsoft.Network/virtualNetworks/subnets/read` `Microsoft.Network/virtualNetworks/subnets/join/action` | Read the customer-supplied subnet configuration and satisfy Azure's cross-resource-group "linked authorization" check. ARO HCP clusters use a customer-supplied (BYO) VNet/subnet rather than an installer-created one, and Azure requires explicit RBAC on a subnet referenced from outside the caller's default scope before an operator can read/reference it. Upstream added this pair specifically for that BYO-VNet scenario, for ingress alongside several other operators. | Customer's cluster subnet                                       |


> Source: [`manifests/00-ingress-credentials-request.yaml`](https://github.com/openshift/cluster-ingress-operator/blob/master/manifests/00-ingress-credentials-request.yaml)
> (the `openshift-ingress-azure` `CredentialsRequest` block) — the upstream
> `CredentialsRequest` the ingress operator submits to the OpenShift Cloud
> Credential Operator; this is the authoritative list of permissions the
> operator actually requests in-cluster.
>
> Cross-reference: the DEV-environment approximation of this role's actions
> in [`dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam`](../../dev-infrastructure/configurations/dev-operator-roles.tmpl.bicepparam)
> ("Azure Red Hat OpenShift Cluster Ingress Operator - Dev") matches this
> list exactly, and the production built-in role (`Azure Red Hat OpenShift
> Cluster Ingress Operator`) grants the same set of actions.

## See Also

- [Operator Identities Index](README.md)
- [`internal/azure/cluster_scoped_identities_config.go`](../../internal/azure/cluster_scoped_identities_config.go)
- [`manifests/00-ingress-credentials-request.yaml`](https://github.com/openshift/cluster-ingress-operator/blob/master/manifests/00-ingress-credentials-request.yaml) — upstream `CredentialsRequest` for the ingress operator
- [`docs/ingress-egress.md`](../ingress-egress.md) — ARO HCP ingress/egress architecture
- [openshift/cluster-ingress-operator](https://github.com/openshift/cluster-ingress-operator) — upstream operator source and docs

