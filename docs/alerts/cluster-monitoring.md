# Cluster Monitoring alerts

Implementation notes for
`observability/alerts/cluster-monitoring-prometheusRule.yaml`
([ARO-26272](https://redhat.atlassian.net/browse/ARO-26272), epic
[ARO-26147](https://redhat.atlassian.net/browse/ARO-26147)).

IcM `runbook_url` points at
[aka.ms/arohcp-runbook-cluster-monitoring](https://aka.ms/arohcp-runbook-cluster-monitoring).

All four alerts ship at severity `"4"` until SAW alert-tester confirms series
presence and firing rates against prod/int hcps datasources.

## Coverage

| Alert | Signal | Notes |
|---|---|---|
| `UJClusterMonitoringOperatorUnavailable` | `cluster_operator_conditions{name="monitoring", condition="available"} == 0` | Worker-gated. Overlaps broadly with `HCPClusterOperatorUnavailable` but scopes to the monitoring CO only. |
| `UJClusterMonitoringOperatorDegraded` | `cluster_operator_conditions{name="monitoring", condition="degraded"} == 1` | Worker-gated. |
| `UJAlertmanagerMainNotReady` | `kube_statefulset_*{statefulset="alertmanager-main", namespace="openshift-monitoring"}` | Guest DP series — may be absent from AMW until DP scrape exists. |
| `UJGuestPrometheusOperatorNotReady` | `prometheus_operator_ready{namespace="openshift-monitoring"}` | Distinct from underlay `PrometheusOperatorNotReady` (`namespace="prometheus"` on services). |

## SAW alert-tester (metric presence first)

```bash
# From alert-tester checkout, b-account + VPN as needed:
./atest grafana --grafana-url "$GRAFANA_URL" --datasource hcps-uksouth \
  -q 'count(cluster_operator_conditions{name="monitoring"})' \
  --from 2026-11-17 --to 2026-11-24

./atest grafana --grafana-url "$GRAFANA_URL" --datasource hcps-uksouth \
  -q 'count(kube_statefulset_replicas{statefulset="alertmanager-main", namespace="openshift-monitoring"})' \
  --from 2026-11-17 --to 2026-11-24

./atest grafana --grafana-url "$GRAFANA_URL" --datasource hcps-uksouth \
  -q 'count(prometheus_operator_ready{namespace="openshift-monitoring"})' \
  --from 2026-11-17 --to 2026-11-24
```

If Alertmanager / guest prometheus-operator queries return empty, keep those
alerts out of paging and document the DP scrape gap on ARO-26272; the CMO
ClusterOperator alerts can still proceed after threshold tuning.
