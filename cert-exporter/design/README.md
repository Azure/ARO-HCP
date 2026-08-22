# Certificate Exporter Design Summary

The certificate exporter runs on both service and management clusters and
publishes certificate lifetime metrics from Kubernetes TLS Secrets.

## At a Glance

| Area | Service cluster | Management cluster |
| --- | --- | --- |
| Certificate scope | TLS Secrets in `aks-istio-ingress` | API and ingress TLS Secrets in approved namespaces |
| Namespace lifecycle | Static | Dynamic as hosted clusters are created and removed |
| Exporter RBAC | Helm-managed RoleBinding | Controller-managed RoleBinding in each approved namespace |
| Secret permissions | `get`, `list`, `watch` | `list`, `watch` |
| Additional component | None | RBAC controller with a ValidatingAdmissionPolicy guard |
| Metrics port | `9793` | `9793`; controller status and metrics on `8081` |

## Security Summary

The exporter is never bound to its Secret-reader ClusterRole with a
ClusterRoleBinding. Both deployments use namespaced RoleBindings, limiting a
compromised exporter identity to approved namespaces.

The service-cluster namespace is fixed, so Helm creates one RoleBinding. The
management-cluster namespaces are dynamic, so a dedicated controller creates
and removes RoleBindings. A fail-closed ValidatingAdmissionPolicy restricts
that controller to the exact role, subject, binding name, and namespace
selectors required by cert-exporter.

## Documents

- [Overall design](OVERVIEW.md) describes both deployments, their security
  model, operations, and tradeoffs.
- [Architecture](ARCHITECTURE.md) contains component, authorization, and
  reconciliation diagrams.
