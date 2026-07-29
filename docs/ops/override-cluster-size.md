# Override Hosted Cluster Control Plane Size

## Problem Description

Hosted Cluster control plane sizing is driven by worker node count via the `ClusterSizingConfiguration` CRD. The default `small` tier (0–60 nodes) limits KAS to `max-requests-inflight=150` and `max-mutating-requests-inflight=50`. Customers running high-concurrency workloads (e.g. 600+ concurrent Helm deployments) can overwhelm these limits, causing etcd Raft buffer overflows, KAS restarts, and control plane outages — even when the node count is low.

## When to Use

- A customer's control plane is being throttled due to API server inflight limits being too low for their workload pattern
- KAS is restarting due to etcd Raft buffer overflows under high concurrency
- You need to increase control plane resources (memory, inflight limits) beyond what the current node-count-based tier provides
- You are responding to an incident where API server throttling is causing customer-visible impact

## Prerequisites

- JIT access to the target environment's service cluster and management cluster
- `kubectl` and `jq` available
- Service cluster access via `hcpctl sc breakglass <sc-name>`
- Management cluster access via `hcpctl mc breakglass <mc-name>` (for verification)

## Size Tiers

Size tiers and their effects (inflight limits, KAS memory, GoMemLimit) are defined in the [`ClusterSizingConfiguration`](../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml).

The Admin API requires **PascalCase** size names: `Small`, `Medium`, `Large`, `Xlarge`, `XXlarge`. Note that the `ClusterSizingConfiguration` CR on the management cluster uses lowercase names (`small`, `medium`, `large`, `xlarge`, `xxlarge`) — do not copy those directly into the Admin API request or it will return a `400` error. This case-sensitivity mismatch is an implementation detail of the Admin API that is outside the scope of this SOP; it will become irrelevant once the Geneva Action frontend is available ([ARO-27690](https://redhat.atlassian.net/browse/ARO-27690)).

To check the currently deployed tiers on a management cluster:

```bash
kubectl get clustersizingconfiguration cluster -o json | \
  jq '.spec.sizes[] | {name, criteria, effects}'
```

> **Important**: The value must be an **exact size name** (e.g. `Large`), **not** a boolean. Setting it to `true` will silently fail — the sizing controller will log an error and apply no size.

---

## Procedure

This procedure uses the Admin API on the service cluster to set a persistent size override. The override is stored in Cosmos DB and propagates through the full Maestro pipeline, so it **survives Maestro reconciliation**.

> **Future**: A Geneva Action frontend for this endpoint is planned ([ARO-27690](https://redhat.atlassian.net/browse/ARO-27690)). Once available, the port-forward and manual curl steps below will be replaced by the Geneva Action workflow.

**Data flow:** Admin API → Cosmos DB (`ServiceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize`) → Backend controller → Cluster Service (`CSPropertySizeOverride`) → Maestro ResourceBundle → ManifestWork → HostedCluster annotation on MC

### Step 1: Get Service Cluster Access

```bash
hcpctl sc breakglass <sc-name>
export KUBECONFIG=<path-from-output>
```

### Step 2: Port-Forward to the Admin API

The Admin API runs in the `aro-hcp-admin-api` namespace on port 8443 (HTTP, not HTTPS):

```bash
kubectl port-forward -n aro-hcp-admin-api deployment/admin-api 8443:8443
```

Leave this running in a separate terminal.

### Step 3: Set the Size Override

The Admin API requires Geneva Actions authentication headers. Construct the request using the cluster's ARM resource path:

```bash
curl -s -X POST \
  "http://localhost:8443/admin/v1/hcp/subscriptions/<subscription-id>/resourcegroups/<resource-group>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster-name>/desiredcontrolplanesize" \
  -H "Content-Type: application/json" \
  -H "X-Ms-Client-Principal-Name: <your-email>" \
  -H "X-Ms-Client-Principal-Type: dstsUser" \
  -d '{"size": "Xlarge"}' | jq .
```

**Parameters:**
- `<subscription-id>`: The Azure subscription ID containing the cluster
- `<resource-group>`: The resource group name
- `<cluster-name>`: The ARM resource name of the cluster
- `<your-email>`: Your email address for audit logging
- `size`: One of `Small`, `Medium`, `Large`, `Xlarge`, `XXlarge`

A `200 OK` response confirms the override was written to Cosmos DB.

> **Important**: The route uses the `/hcp` prefix (`/admin/v1/hcp/subscriptions/...`), not `/admin/v1/subscriptions/...`.

> **Important**: Port 8443 serves HTTP, not HTTPS. Using `https://` will produce a TLS error.

### Step 4: Verify on the Management Cluster

The override propagates through the full pipeline (Cosmos DB → Backend → Cluster Service → Maestro → ManifestWork → HostedCluster). This typically takes 1–3 minutes.

Get management cluster access and verify (see [Identifying the HostedCluster](#identifying-the-hostedcluster-on-the-management-cluster) for how to find the HC name):

```bash
hcpctl mc breakglass <mc-name>
export KUBECONFIG=<path-from-output>

HC_NS="<namespace>"
HC_NAME="<name>"

kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" \
  -o json | jq '{
    size: .metadata.labels["hypershift.openshift.io/hosted-cluster-size"],
    sizeOverride: .metadata.annotations["hypershift.openshift.io/cluster-size-override"],
    maxRequests: .metadata.annotations["hypershift.openshift.io/kube-apiserver-max-requests-inflight"],
    maxMutating: .metadata.annotations["hypershift.openshift.io/kube-apiserver-max-mutating-requests-inflight"],
    kasMemory: .metadata.annotations["resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver"]
  }'
```

Confirm KAS rolled out with the new settings:

```bash
CP_NS="${HC_NS}-${HC_NAME}"
kubectl rollout status deployment/kube-apiserver -n "$CP_NS"
kubectl get pods -l app=kube-apiserver -n "$CP_NS" -o wide
```

All 3 KAS replicas should be `Running` with all containers ready (typically `6/6`).

> **If the HostedCluster still shows the old size after 5 minutes**, check for a SSA field ownership conflict — see [Troubleshooting: SSA Field Ownership Conflict](#ssa-field-ownership-conflict-after-prior-manual-annotation).

After the override is confirmed, monitor the [Grafana APF dashboard](../../observability/grafana-dashboards/perfscale-dashboards/api-performance.json) (`apiserver_flowcontrol_*` metrics) to verify the new inflight limits are in effect under load.

## Rollback

To remove the override and let the cluster revert to node-count-based sizing, send `null` for the size:

```bash
curl -s -X POST \
  "http://localhost:8443/admin/v1/hcp/subscriptions/<subscription-id>/resourcegroups/<resource-group>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster-name>/desiredcontrolplanesize" \
  -H "Content-Type: application/json" \
  -H "X-Ms-Client-Principal-Name: <your-email>" \
  -H "X-Ms-Client-Principal-Type: dstsUser" \
  -d '{"size": null}' | jq .
```

> **Note**: The `transitionDelay.decrease` is `20m`, so the cluster will take up to 20 minutes to scale back down after the override is cleared.

---

## Identifying the HostedCluster on the Management Cluster

HostedCluster names on the management cluster are opaque internal IDs (e.g. `c8h1q2z8b4v8f3d`), not the ARM resource name or display name. You must search by subscription ID, ARM resource name, or the `api.openshift.com/name` label.

### Search by subscription ID or resource name

```bash
kubectl get hostedclusters -A -o json | \
  jq -r '.items[] | select(tostring | test("<subscription-id-or-resource-name>")) |
    "\(.metadata.namespace)/\(.metadata.name)  \(.metadata.labels["api.openshift.com/name"] // "unknown")"'
```

### Search by display name label

```bash
kubectl get hostedclusters -A -l "api.openshift.com/name=<display-name>" \
  -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name'
```

### Namespace conventions

- **HostedCluster namespace**: `ocm-<cluster-prefix>-<cluster-id>`
- **Control plane namespace**: `ocm-<cluster-prefix>-<cluster-id>-<hc-name>`
- **Cluster prefix by environment**: `arohcpint` (int), `arohcpstg` (stage), `arohcpprod` (prod)

---

## Troubleshooting

### SSA Field Ownership Conflict (after prior manual annotation)

If someone previously used `kubectl annotate` to set the `cluster-size-override` annotation directly on the HostedCluster, the Admin API override may fail at the ManifestWork apply step. **Do not manually annotate HostedClusters — always use the Admin API.**

**Symptom:** Admin API returns `200 OK`, but the HostedCluster on the MC still shows the old size. The ManifestWork condition shows:

```
Failed to apply manifest:
Apply failed with 1 conflict:
conflict with "kubectl-annotate" using hypershift.openshift.io/v1beta1:
.metadata.annotations.hypershift.openshift.io/cluster-size-override
```

**Root cause:** `kubectl annotate` registers `kubectl-annotate` as the SSA field manager for the annotation. When ManifestWork tries to apply the same annotation via server-side apply, Kubernetes rejects it due to the field ownership conflict.

**Check for the conflict:**

```bash
kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" -o json | \
  jq '.metadata.managedFields[] | select(.manager=="kubectl-annotate") | {manager, operation, time}'
```

If this returns results, the conflict exists.

**Fix:** Remove the annotation to release field manager ownership, then ManifestWork will immediately reapply it:

```bash
kubectl annotate hostedcluster "$HC_NAME" -n "$HC_NS" \
  hypershift.openshift.io/cluster-size-override-
```

Watch for ManifestWork to reapply:

```bash
kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" -w \
  -o jsonpath='{.metadata.annotations.hypershift\.openshift\.io/cluster-size-override}{"\t"}{.metadata.labels.hypershift\.openshift\.io/hosted-cluster-size}{"\n"}'
```

The annotation will briefly disappear, then ManifestWork will reapply it with the correct value within seconds.

### ManifestWork Not Updating

If the Admin API returns `200 OK` but the ManifestWork on the MC does not contain the new size override, the pipeline has stalled between Cosmos DB and Maestro. Check:

1. **Backend controller logs** (service cluster): `kubectl logs -n aro-hcp deployment/backend -c backend --since=10m | grep -i "size"`
2. **Cluster Service ResourceBundle**: Port-forward to Maestro and check the ResourceBundle contains the updated annotation
3. **ManifestWork content**: `kubectl get manifestwork -n local-cluster -o json | jq '.items[] | select(.spec.workload.manifests[]? | select(.kind=="HostedCluster" and .metadata.name=="<hc-name>")) | .status.conditions'`

---

## Related Components

- **ClusterSizingConfiguration**: [hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml](../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml) — defines available size tiers and their effects
- **Admin API handler**: [admin/server/handlers/hcp/desiredcontrolplanesize.go](../../admin/server/handlers/hcp/desiredcontrolplanesize.go) — writes `DesiredHostedClusterControlPlaneSize` to Cosmos DB
- **Backend syncer**: [backend/pkg/controllers/clusterpropertiescontroller/desired_control_plane_size_sync.go](../../backend/pkg/controllers/clusterpropertiescontroller/desired_control_plane_size_sync.go) — syncs size override to Cluster Service
- **ARM tag admission**: [internal/admission/admit_cluster.go](../../internal/admission/admit_cluster.go) — handles `aro-hcp.experimental.cluster.size-override` tag
- **HyperShift sizing controller**: upstream `hostedclustersizing_controller.go` — consumes the annotation
- **Grafana APF dashboard**: `observability/grafana-dashboards/perfscale-dashboards/api-performance.json` — monitor `apiserver_flowcontrol_*` metrics after override
- **Admin API feature tracking**: [ARO-27690](https://redhat.atlassian.net/browse/ARO-27690) — Expose cluster-size-override via Admin API (includes future Geneva Action work)

## Production References

- **ARO-27679**: First validated on `jude-hcp-eastus2` — ephemeral override Small → Large during Adobe load testing incident (IcM 814707269)
- **ARO-28258**: Admin API persistent override validated on `jude-hcp-eastus2` — Small → Xlarge via `POST /desiredcontrolplanesize`, confirmed durable across ~5 hours of Maestro reconciliation (July 2025)
- **ARO-28342**: Production resize of `arohcp4` (Canada Central) — Large → Xlarge via Admin API for Adobe/IBM customer APF throttling. Encountered and resolved SSA field ownership conflict from prior manual annotation (July 2025)
