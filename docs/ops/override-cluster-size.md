# Override Hosted Cluster Control Plane Size

## Problem Description

Hosted Cluster control plane sizing is driven by worker node count via the `ClusterSizingConfiguration` CRD. The default `small` tier (0–60 nodes) limits KAS to `max-requests-inflight=150` and `max-mutating-requests-inflight=50`. Customers running high-concurrency workloads (e.g. 600+ concurrent Helm deployments) can overwhelm these limits, causing etcd Raft buffer overflows, KAS restarts, and control plane outages — even when the node count is low.

## When to Use

- A customer's control plane is being throttled due to API server inflight limits being too low for their workload pattern
- KAS is restarting due to etcd Raft buffer overflows under high concurrency
- You need to increase control plane resources (memory, inflight limits) beyond what the current node-count-based tier provides
- You are responding to an incident where API server throttling is causing customer-visible impact

## Available Methods

| Method | Persistence | Use When |
|--------|------------|----------|
| **Option A: Admin API** (recommended) | Persistent — survives Maestro reconciliation | Production overrides that must remain durable |
| **Option B: Direct Annotation** | Ephemeral — may be reverted by Maestro | Quick debugging, or when the Admin API is unavailable |

## Prerequisites

- JIT access to the target environment's service cluster and/or management cluster
- `kubectl` and `jq` available
- For Option A: Service cluster access via `hcpctl sc breakglass <sc-name>`
- For Option B: Management cluster access via `hcpctl mc breakglass <mc-name>`

## Size Tiers

Size tiers and their effects (inflight limits, KAS memory, GoMemLimit) are defined in the [`ClusterSizingConfiguration`](../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml). Valid size names are: `Small`, `Medium`, `Large`, `Xlarge`, `XXlarge` (Admin API, case-sensitive) or lowercase equivalents for direct annotation.

To check the currently deployed tiers on a management cluster:

```bash
kubectl get clustersizingconfiguration cluster -o json | \
  jq '.spec.sizes[] | {name, criteria, effects}'
```

> **Important**: The value must be an **exact size name** (e.g. `Large`), **not** a boolean. Setting it to `true` will silently fail — the sizing controller will log an error and apply no size.

---

## Option A: Admin API (Persistent — Recommended)

This method calls the Admin API on the service cluster, which writes the desired size to the `ServiceProviderCluster` document in Cosmos DB. The backend controller syncs this to Cluster Service, which propagates it through Maestro to the management cluster. The override **survives Maestro reconciliation** because it is part of the source-of-truth pipeline.

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

### Step 4: Clear the Override (Rollback)

To remove the override and let the cluster revert to node-count-based sizing, send `null` for the size:

```bash
curl -s -X POST \
  "http://localhost:8443/admin/v1/hcp/subscriptions/<subscription-id>/resourcegroups/<resource-group>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster-name>/desiredcontrolplanesize" \
  -H "Content-Type: application/json" \
  -H "X-Ms-Client-Principal-Name: <your-email>" \
  -H "X-Ms-Client-Principal-Type: dstsUser" \
  -d '{"size": null}' | jq .
```

### Step 5: Verify on the Management Cluster

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

After the override is confirmed, monitor the [Grafana APF dashboard](../../observability/grafana-dashboards/perfscale-dashboards/api-performance.json) (`apiserver_flowcontrol_*` metrics) to verify the new inflight limits are in effect under load.

---

## Option B: Direct Annotation (Ephemeral)

This method annotates the HostedCluster directly on the management cluster. The override takes effect immediately but **may be reverted by Maestro** on the next ResourceBundle reconciliation (e.g. during upgrades or config changes), because the annotation is not in the Maestro source pipeline.

Use this method only for quick debugging or when the Admin API is unavailable.

### Step 1: Get Management Cluster Access

```bash
hcpctl mc breakglass <mc-name>
export KUBECONFIG=<path-from-output>
```

### Step 2: Identify the HostedCluster

See [Identifying the HostedCluster](#identifying-the-hostedcluster-on-the-management-cluster) below.

### Step 3: Check Current Size

```bash
HC_NS="<namespace>"
HC_NAME="<name>"

kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" \
  -o json | jq '{
    size: .metadata.labels["hypershift.openshift.io/hosted-cluster-size"],
    maxRequests: .metadata.annotations["hypershift.openshift.io/kube-apiserver-max-requests-inflight"],
    maxMutating: .metadata.annotations["hypershift.openshift.io/kube-apiserver-max-mutating-requests-inflight"],
    kasMemory: .metadata.annotations["resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver"]
  }'
```

### Step 4: Apply the Size Override

```bash
kubectl annotate hostedcluster "$HC_NAME" -n "$HC_NS" \
  hypershift.openshift.io/cluster-size-override=large --overwrite
```

### Step 5: Verify the Override Took Effect

The sizing controller reconciles with `increase` delay of `0s`, so changes apply quickly.

```bash
# Confirm label changed
kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" \
  -o jsonpath='{.metadata.labels.hypershift\.openshift\.io/hosted-cluster-size}'

# Confirm annotations updated
kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" \
  -o json | jq '.metadata.annotations | with_entries(select(.key | test("max-requests|max-mutating|resource-request-override|gomemlimit")))'
```

After confirming, monitor the [Grafana APF dashboard](../../observability/grafana-dashboards/perfscale-dashboards/api-performance.json) (`apiserver_flowcontrol_*` metrics) to verify the new limits under load.

### Step 6: Verify KAS Rollout

```bash
CP_NS="${HC_NS}-${HC_NAME}"
kubectl rollout status deployment/kube-apiserver -n "$CP_NS"
kubectl get pods -l app=kube-apiserver -n "$CP_NS" -o wide
```

### Durability Warning

The HostedCluster is owned by a Maestro `AppliedManifestWork`. The `cluster-size-override` annotation is **not** in the original ResourceBundle, so Maestro may strip it on the next reconciliation.

To watch whether the annotation persists:

```bash
kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" -w \
  -o jsonpath='{.metadata.annotations.hypershift\.openshift\.io/cluster-size-override}{"\t"}{.metadata.labels.hypershift\.openshift\.io/hosted-cluster-size}{"\n"}'
```

If durability is required, switch to [Option A](#option-a-admin-api-persistent--recommended).

### Rollback

To remove the annotation (the trailing `-` is kubectl syntax to delete an annotation):

```bash
kubectl annotate hostedcluster "$HC_NAME" -n "$HC_NS" \
  hypershift.openshift.io/cluster-size-override- --overwrite
```

> **Note**: The `transitionDelay.decrease` is `20m`, so the cluster will take up to 20 minutes to scale back down after the annotation is removed.

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

## Related Components

- **ClusterSizingConfiguration**: [hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml](../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml) — defines available size tiers and their effects
- **Admin API handler**: [admin/server/handlers/hcp/desiredcontrolplanesize.go](../../admin/server/handlers/hcp/desiredcontrolplanesize.go) — writes `DesiredHostedClusterControlPlaneSize` to Cosmos DB
- **Backend syncer**: [backend/pkg/controllers/clusterpropertiescontroller/desired_control_plane_size_sync.go](../../backend/pkg/controllers/clusterpropertiescontroller/desired_control_plane_size_sync.go) — syncs size override to Cluster Service
- **ARM tag admission**: [internal/admission/admit_cluster.go](../../internal/admission/admit_cluster.go) — handles `aro-hcp.experimental.cluster.size-override` tag
- **HyperShift sizing controller**: upstream `hostedclustersizing_controller.go` — consumes the annotation
- **Grafana APF dashboard**: `observability/grafana-dashboards/perfscale-dashboards/api-performance.json` — monitor `apiserver_flowcontrol_*` metrics after override
- **Admin API feature tracking**: [ARO-27690](https://redhat.atlassian.net/browse/ARO-27690) — Expose cluster-size-override via Admin API

## Production References

- **ARO-27679**: First validated on `jude-hcp-eastus2` — ephemeral override `small` → `large` during Adobe load testing incident (IcM 814707269)
- **ARO-28258**: Admin API persistent override validated on `jude-hcp-eastus2` — `small` → `xlarge` via `POST /desiredcontrolplanesize`, confirmed durable across ~5 hours of Maestro reconciliation (July 2025)
