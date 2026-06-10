# Override Hosted Cluster Control Plane Size

## Problem Description

Hosted Cluster control plane sizing is driven by worker node count via the `ClusterSizingConfiguration` CRD. The default `small` tier (0–60 nodes) limits KAS to `max-requests-inflight=150` and `max-mutating-requests-inflight=50`. Customers running high-concurrency workloads (e.g. 600+ concurrent Helm deployments) can overwhelm these limits, causing etcd Raft buffer overflows, KAS restarts, and control plane outages — even when the node count is low.

The `hypershift.openshift.io/cluster-size-override` annotation on the HostedCluster allows overriding the node-count-based sizing to a larger tier. This is the highest-priority input to the HyperShift sizing controller and takes effect immediately (`transitionDelay.increase: 0s`).

## Prerequisites

- Management cluster breakglass access via `hcpctl mc breakglass <mc-name>`
- `kubectl` and `jq` available

## Size Tiers

| Size | Node Range | `maxRequestsInflight` | `maxMutatingInflight` | KAS Memory | `kasGoMemLimit` |
|------|-----------|----------------------|----------------------|------------|-----------------|
| `small` | 0–60 | 150 | 50 | 8Gi | — |
| `medium` | 61–120 | 900 | 300 | 16Gi | 12GiB |
| `large` | 121–252 | 1,200 | 400 | 32Gi | 24GiB |
| `xlarge` | 253–360 | _(not set)_ | _(not set)_ | 64Gi | 48GiB |
| `xxlarge` | 361–999998 | _(not set)_ | _(not set)_ | 96Gi | 72GiB |

> **Note**: `xlarge` and `xxlarge` do not set explicit inflight limits — they fall back to operator defaults. `large` is the highest tier with explicit APF-relevant limits (1200/400).

> **Important**: The annotation value must be an **exact size name** (e.g. `large`), **not** a boolean. Setting it to `true` will silently fail — the sizing controller will log an error and apply no size.

## Procedure

### Step 1: Get Management Cluster Access

```bash
hcpctl mc breakglass <mc-name>
export KUBECONFIG=<path-from-output>
```

### Step 2: Identify the HostedCluster

The HostedCluster name and namespace use internal IDs, not the ARM resource name. Search by the ARM resource ID or subscription ID:

```bash
# Search by ARM resource name or subscription ID
kubectl get hostedclusters -A -o json | \
  jq -r '.items[] | select(tostring | test("<subscription-id-or-resource-name>")) |
    "\(.metadata.namespace)/\(.metadata.name)  \(.metadata.labels["api.openshift.com/name"] // "unknown")"'
```

Example:

```bash
kubectl get hostedclusters -A -o json | \
  jq -r '.items[] | select(tostring | test("3ceade1e")) |
    "\(.metadata.namespace)/\(.metadata.name)  \(.metadata.labels["api.openshift.com/name"] // "unknown")"'
# ocm-arohcpprod-2qah3ue0eia4udhrrh06holch0u3baim/y6g4d7i9d5s9s1u  jude-hcp-eastus2
```

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
# Expected: large

# Confirm annotations updated
kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" \
  -o json | jq '.metadata.annotations | with_entries(select(.key | test("max-requests|max-mutating|resource-request-override|gomemlimit")))'
```

### Step 6: Verify KAS Rollout

The control plane namespace follows the pattern `<hc-namespace>-<hc-name>`:

```bash
# Find the CP namespace
kubectl get namespaces | grep "${HC_NS##*-}"

# Check KAS rollout status
CP_NS="${HC_NS}-${HC_NAME}"
kubectl rollout status deployment/kube-apiserver -n "$CP_NS"

# Confirm all KAS pods are running
kubectl get pods -l app=kube-apiserver -n "$CP_NS" -o wide
```

## Durability Caveat

The HostedCluster is owned by a Maestro `AppliedManifestWork`. The `cluster-size-override` annotation is **not** in the original ResourceBundle, so Maestro may strip it on the next reconciliation (triggered by a Clusters Service ResourceBundle update, e.g. during upgrades or config changes).

**To check if the annotation persists:**

```bash
# Watch the annotation in real-time
kubectl get hostedcluster "$HC_NAME" -n "$HC_NS" -w \
  -o jsonpath='{.metadata.annotations.hypershift\.openshift\.io/cluster-size-override}{"\t"}{.metadata.labels.hypershift\.openshift\.io/hosted-cluster-size}{"\n"}'
```

**To verify the annotation is not in the ManifestWork source (expected):**

```bash
kubectl get manifestwork -n local-cluster | grep "${HC_NS##*ocm-arohcpprod-}"

kubectl get manifestwork <name> -n local-cluster -o json | \
  jq '.spec.workload.manifests[] | select(.kind=="HostedCluster") |
    .metadata.annotations | keys[] | select(test("cluster-size-override"))'
# Empty output confirms the annotation is not in the source → Maestro may revert it
```

**For a durable fix**, the override must be set at the Clusters Service level via the `hosted_cluster_size_override` property, so it flows through the Maestro pipeline. This currently only supports `Minimal` (downsize) via the ARM tag `aro-hcp.experimental.cluster.size-override`. Upsize overrides persisted through CS are not yet supported — this is tracked as a feature gap.

## Rollback

To remove the override and let the cluster revert to node-count-based sizing:

```bash
kubectl annotate hostedcluster "$HC_NAME" -n "$HC_NS" \
  hypershift.openshift.io/cluster-size-override- --overwrite
```

> **Note**: The `transitionDelay.decrease` is `20m`, so the cluster will take up to 20 minutes to scale back down after the annotation is removed.

## Related Components

- **ClusterSizingConfiguration**: [hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml](../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml)
- **ARM tag admission**: [internal/admission/admit_cluster.go](../../internal/admission/admit_cluster.go) — handles `aro-hcp.experimental.cluster.size-override` tag
- **CS property conversion**: [internal/ocm/convert.go](../../internal/ocm/convert.go) — translates to `hosted_cluster_size_override` CS property
- **HyperShift sizing controller**: upstream `hostedclustersizing_controller.go` — consumes the annotation
- **Grafana APF dashboard**: `observability/grafana-dashboards/perfscale-dashboards/api-performance.json` — monitor `apiserver_flowcontrol_*` metrics after override

## Reference: ARO-27679

First validated on production cluster `jude-hcp-eastus2` (Adobe load testing incident, IcM 814707269). Override from `small` → `large` applied successfully, KAS rolled out with new inflight limits (150→1200, 50→400).
