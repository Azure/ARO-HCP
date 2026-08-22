# Certificate Exporter Overall Design

## Status

Implemented for service and management clusters.

## Purpose

ARO HCP uses the upstream x509 certificate exporter to expose certificate
validity and expiry information as Prometheus metrics. The exporter reads PEM
certificates from Kubernetes TLS Secrets and serves metrics on port `9793`.

The same exporter image is deployed to both cluster types, but certificate
locations and namespace lifecycles differ:

- Service clusters monitor TLS Secrets in the fixed `aks-istio-ingress`
  namespace.
- Management clusters monitor `kube-apiserver-tls-cert` in dynamically created
  `ocm-*` namespaces and `default-ingress-tls-cert-*` in the fixed
  `open-cluster-management-policies` namespace.

## Security Problem

The exporter requires Kubernetes API access to Secret data. Its configuration
filters certificate types and Secret names, but that filtering happens inside
the exporter after Kubernetes authorization.

A ClusterRoleBinding to the Secret-reader ClusterRole would authorize the
exporter in every namespace. If the exporter container or ServiceAccount token
were compromised, an attacker could enumerate unrelated cluster Secrets. The
design therefore uses a reusable ClusterRole but grants it only through
namespaced RoleBindings.

A RoleBinding may reference a ClusterRole. The referenced rules remain limited
to the RoleBinding's namespace, providing namespace isolation without
duplicating a Role in every namespace.

## Shared Deployment Model

Both cluster variants install:

- A dedicated `cert-exporter` ServiceAccount.
- A Secret-reader ClusterRole.
- One cert-exporter Deployment and configuration ConfigMap.
- A Service and ServiceMonitor for Prometheus collection.
- A digest-pinned exporter image mirrored into the environment service ACR.
- An ACR pull binding backed by workload identity.

The exporter containers run as non-root with a read-only root filesystem,
runtime-default seccomp, privilege escalation disabled, and all Linux
capabilities dropped.

## Service Cluster Design

### Certificate selection

The service-cluster exporter watches the `aks-istio-ingress` namespace. It
processes `tls.crt` from every Secret of type `kubernetes.io/tls` in that
namespace.

### Authorization

The service-cluster Secret-reader ClusterRole grants `get`, `list`, and `watch`
on Secrets. Helm creates one RoleBinding in `aks-istio-ingress` that references
the ClusterRole and names the exporter ServiceAccount in the cert-exporter
release namespace.

The target namespace is static and exists independently of hosted-cluster
lifecycle, so no dynamic RBAC component is required.

### Metrics access

An Istio AuthorizationPolicy permits `GET /metrics` on port `9793`. The
ServiceMonitor discovers the exporter Service for collection.

## Management Cluster Design

### Certificate selection

The management-cluster exporter has two sources:

- `kube-apiserver-tls-cert` Secrets in namespaces beginning with `ocm-`.
- `default-ingress-tls-cert-*` Secrets in
  `open-cluster-management-policies`.

Only `tls.crt` fields from `kubernetes.io/tls` Secrets are parsed. The
management Secret-reader ClusterRole grants `list` and `watch`, not `get`.

### Why a controller is required

Control-plane namespaces appear and disappear with hosted clusters and are not
known when the Helm release is rendered. Static RoleBindings cannot cover
future namespaces, while a ClusterRoleBinding would grant excessive access.

The management release therefore deploys a separate RBAC controller. It lists
namespaces and creates a RoleBinding named `cert-exporter` in namespaces that:

- Start with `ocm-`; or
- Equal `open-cluster-management-policies`.

Each generated RoleBinding has a fixed shape:

- Label: `app.kubernetes.io/managed-by: cert-exporter-rbac-controller`.
- Role reference: the `cert-exporter` ClusterRole.
- Subject: the exporter ServiceAccount in the Helm release namespace.

The exporter and controller use separate ServiceAccounts. The controller has
no permission to read Secrets.

### Controller permissions

| Resource | Scope | Verbs | Constraint |
| --- | --- | --- | --- |
| Namespaces | Cluster | `list` | Used only for namespace selection. |
| RoleBindings | Namespaced | `create` | Create cannot be restricted by `resourceNames`. |
| RoleBindings | Namespaced | `get`, `update`, `delete` | Restricted to `cert-exporter`. |
| ClusterRoles | Cluster | `bind` | Restricted to the `cert-exporter` ClusterRole. |

The `bind` permission is required to create RoleBindings that reference the
Secret-reader ClusterRole. The broad shape of RoleBinding create authorization
cannot be fully constrained with RBAC alone.

### Admission defense

A ValidatingAdmissionPolicy applies to RoleBinding create, update, and delete
requests made by the controller ServiceAccount. It uses `failurePolicy: Fail`
and permits only:

- Create or update in a selected namespace.
- The exact RoleBinding name and ownership label.
- A reference to the `cert-exporter` ClusterRole.
- A single subject matching the exporter ServiceAccount and release namespace.
- Deletion of a controller-owned binding, including cleanup from a namespace
  that is no longer selected.

This policy limits the impact of a compromised controller. Its `create` and
`bind` privileges cannot be used to grant another role, authorize another
subject, or create bindings in unrelated namespaces.

### Reconciliation

The controller reconciles at startup and once per minute:

1. List all namespaces.
2. Create missing RoleBindings in selected namespaces.
3. Refuse to modify a same-named binding without the ownership label.
4. Update owned bindings when labels or subjects drift.
5. Delete and recreate an owned binding when its immutable `roleRef` drifts.
6. Delete owned bindings from namespaces that no longer match.

Errors are collected per namespace so one failure does not prevent processing
other namespaces. Failures are retried on the next interval.

### Failure behavior

- Existing RoleBindings remain effective while the controller is unavailable.
- New target namespaces wait for controller recovery before exporter access is
  granted.
- A missing policy namespace produces a warning but does not stop prefix-based
  reconciliation.
- An unmanaged binding collision produces an error and is not taken over.
- Admission evaluation failures deny controller mutations.
- Namespace deletion removes namespaced RoleBindings through Kubernetes
  garbage collection.

### Controller observability

The controller serves:

- `/healthz` when a reconciliation attempt occurred recently.
- `/readyz` when a reconciliation succeeded recently.
- `/metrics` with target namespace count, RoleBinding mutation counters, and
  reconciliation error count.

Freshness becomes stale after three reconciliation intervals. Structured logs
record mutations, missing expected namespaces, collisions, and errors. A
ServiceMonitor collects the controller metrics.

## Security Properties

- The exporter has no cluster-wide Secret binding.
- Service-cluster Secret access is limited to `aks-istio-ingress`.
- Management-cluster Secret access is limited to selected namespaces.
- The management controller cannot read Secrets.
- The controller may bind only the cert-exporter ClusterRole.
- Admission validates the complete RoleBinding shape for controller requests.
- Unmanaged RoleBindings are never silently adopted.
- Exporter and controller images are digest-pinned.

## Residual Risks

- Within an approved namespace, the exporter can read more Secrets than its
  client-side filters process. Kubernetes RBAC cannot safely combine wildcard
  discovery with Secret-name-restricted list/watch access.
- Any namespace beginning with `ocm-` enters the management authorization
  scope. The design relies on the management-cluster namespace naming contract.
- The admission policy constrains only requests made as the controller
  identity. Other privileged identities remain governed by their own RBAC and
  admission controls.
- Existing management RoleBindings remain effective during controller outage,
  favoring monitoring availability over immediate revocation.

## Alternatives Considered

### ClusterRoleBinding for the exporter

Rejected because it exposes Secret data from unrelated namespaces on either
cluster type.

### One Role per management namespace

Rejected because the controller would need permission to create or escalate
Roles containing Secret permissions. A fixed ClusterRole keeps permission
rules static while only RoleBindings are dynamic.

### Static management RoleBindings

Rejected because hosted-cluster namespaces are created dynamically after Helm
rendering.

### Secret-name-restricted RBAC

Rejected because the exporter relies on list/watch discovery. Kubernetes
`resourceNames` does not support wildcard names and does not generally restrict
list/watch without matching field selectors.

## Source Locations

- Service-cluster chart: [`../svc/deploy`](../svc/deploy)
- Management-cluster chart: [`../mgmt/deploy`](../mgmt/deploy)
- RBAC controller: [`../controller.go`](../controller.go)
- Controller runtime and probes: [`../main.go`](../main.go)
- Controller metrics: [`../status.go`](../status.go)
