Load all of CLAUDE.md for context

## API Field Documentation

Every API struct field must have GoDoc explaining its meaning and when and why
it is populated. Include readiness, compatibility, or retirement semantics where
relevant. For persisted fields, keep the `Written by:` trailer after that
description; the writer annotation alone is not sufficient documentation.

## Image Bumps

For all image bump related tasks, see [tooling/image-updater/AGENTS.md](tooling/image-updater/AGENTS.md).

## Operational Troubleshooting Context

When assisting with operational troubleshooting tasks (stuck clusters, deletion issues, resource cleanup), the following context is essential.

### Cluster Access

- **Management Cluster**: `hcpctl mc breakglass <mc-name>` — writes kubeconfig to a temp file, you must `export KUBECONFIG=<path>` from the output
- **Service Cluster**: `hcpctl sc breakglass <svc-name>` — same pattern, separate kubeconfig file
- The kubeconfig paths are temporary and expire. If you get connection errors, the user needs to re-authenticate.
- Context names follow the pattern: `int-uksouth-mgmt-1`, `int-uksouth-svc-1`, etc.

### Architecture: Deletion Flow

The deletion chain flows top-down through these layers. Deleting resources at a lower layer without removing the source above will cause them to be **recreated**:

```
Clusters Service (API) → Maestro Server (ResourceBundle) → Maestro Agent → ManifestWork → HostedCluster → Control Plane
```

### Service Cluster APIs

These are REST APIs accessed via `kubectl exec`, not CRDs:

- **Clusters Service** (ns: `clusters-service`, deployment: `clusters-service`, port 8000):
  - List clusters: `GET /api/clusters_mgmt/v1/clusters`
  - Get cluster: `GET /api/clusters_mgmt/v1/clusters/<id>`
  - Delete cluster: `DELETE /api/aro_hcp/v1alpha1/clusters/<id>` (MUST use `aro_hcp` endpoint, NOT `clusters_mgmt`)
  - Container name: `clusters-service-server` (use with `kubectl exec -c clusters-service-server`)

- **Maestro Server** (ns: `maestro`, deployment: `maestro`, port 8000):
  - List resource bundles: `GET /api/maestro/v1/resource-bundles`
  - Get bundle: `GET /api/maestro/v1/resource-bundles/<id>`
  - Delete bundle: `DELETE /api/maestro/v1/resource-bundles/<id>`
  - Container name: `maestro-server`
  - Resource bundles use UUIDs as names, not cluster IDs. Search by filtering manifest content with jq: `jq --arg cid "<cluster-id>" '.items[] | select(.manifests | tostring | test($cid))'`
  - Namespace-only bundles have label `containsNamespaces: true` and create `*-00-hcp-namespaces` ManifestWork

### Management Cluster Resources

- **Namespaces**: `ocm-${CLUSTER_PREFIX}-${CLUSTER_ID}` (HostedCluster ns) and `ocm-${CLUSTER_PREFIX}-${CLUSTER_ID}-${CLUSTER_NAME}` (Control Plane ns)
- **CLUSTER_PREFIX**: `arohcpint` (int), `arohcpstg` (stage), `arohcpprod` (prod)
- **ManifestWork** in `local-cluster` namespace, named by cluster ID
- **ManagedCluster** is cluster-scoped, named by cluster ID

### Common Finalizers That Block Deletion

| Finalizer | Resource | Controller |
|---|---|---|
| `hypershift.openshift.io/finalizer` | HostedCluster, HostedControlPlane | Hypershift Operator |
| `hypershift.openshift.io/component-finalizer` | Deployments in CP namespace | Hypershift Operator |
| `cluster.cluster.x-k8s.io` | Cluster (CAPI) | Cluster API |
| `cluster.open-cluster-management.io/manifest-work-cleanup` | ManifestWork | ACM Work Agent |

### Known Issues

- **Orphaned namespace bundles**: When CS deletes a cluster, it may leave behind the `*-00-hcp-namespaces` ResourceBundle in Maestro. This causes the Maestro Agent to keep recreating the ManifestWork and namespace on the management cluster. Fix: delete the orphaned ResourceBundle from Maestro directly.
- **Azure resources already gone**: If the Azure resource group was deleted externally, the Hypershift operator will loop endlessly trying to clean up non-existent resources. The `hypershift.openshift.io/finalizer` must be removed manually.
- **CP namespace stuck Terminating**: Check `kubectl get namespace <ns> -o json | jq '.status.conditions'` — the `NamespaceContentRemaining` condition lists exactly which resources and finalizers are blocking.

### Operational Docs

- [Cleanup Stuck Cluster Deletion](docs/ops/cleanup-stuck-cluster-deletion.md) — full troubleshooting procedure
- [Fix Maestro Stale Resource Bundle](docs/ops/fix-maestro-stale-resource-bundle.md)
- [HCP Cluster Creation Flow](docs/ops/hcp-cluster-creation-flow.md) — includes component debug commands
- [Postgres Breakglass](docs/ops/postgres-breakglass.md)
