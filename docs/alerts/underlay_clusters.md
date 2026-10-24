# underlay_clusters alerts

The `underlay_clusters` metric is emitted by the static recording rule defined in
`dev-infrastructure/modules/metrics/underlay-clusters-metric.bicep`.
Each service and management cluster deployment instantiates that module with its
AKS cluster name, producing:

```promql
underlay_clusters{cluster="<cluster-name>", source="bicep"} = 1
```

This metric is the source of truth for which underlay service and management
clusters should exist. It is static deployment-time inventory, so it should
always be present for every service cluster and every management cluster, and
entries should not disappear while those clusters still exist.

If an `underlay_clusters` entry disappears, or the inventory becomes too small,
multiple other alerts lose their desired-cluster source of truth and may stop
detecting cluster-specific failures. Treat these alerts as requiring immediate
attention.

## Inventory-driven discovery

`ArobitForwarderJobUp`, `MiseEnvoyScrapeDown`, and
`MaestroAgentMetricsProxyDown` enumerate expected clusters from this inventory.
MISE is restricted to service clusters; Maestro agent is restricted to management
clusters. Their health checks use workload readiness rather than `up` scrape
metrics. These checks detect missing readiness telemetry as well as unready
workloads; a Ready workload does not prove that its metrics endpoint is healthy.

The shared `KubeAPIDown`, `KubeletDown`, `KubeSchedulerDown`, and
`KubeControllerManagerDown` definitions use `kube_node_info` for hosted-cluster
discovery. `observability/alerts-kusto-services.yaml` replaces only their
discovery operand with `underlay_clusters` when generating services rules.
Hosted-control-plane rules retain `kube_node_info`: underlay inventory does not
describe customer clusters. Both variants use component metrics, not `up`, on
the right-hand side of `unless`.

## Alerts

### UnderlayClusterInventoryEntryMissing

This alert fires when a `cluster` label existed in `underlay_clusters` within
the last 7 days but is no longer present.

Check whether the affected service or management cluster was intentionally
removed. If it still exists, investigate the cluster deployment and the
`underlay-clusters-metric-<cluster>` recording rule in the services Azure
Monitor Workspace.

### UnderlayClusterInventoryTooSmall

This alert fires when `underlay_clusters` has two or fewer distinct `cluster`
label values, or when the `underlay_clusters` metric itself is absent.

The inventory should never be this small in a healthy environment. Confirm the
underlay-clusters metric rule groups exist for the service cluster and all
management cluster stamps, then restore the missing recording rules before
dependent alerts lose coverage.
