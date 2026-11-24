# Cluster Monitoring alerts

Implementation notes for
`observability/alerts/cluster-monitoring-prometheusRule.yaml`
([ARO-26272](https://redhat.atlassian.net/browse/ARO-26272), epic
[ARO-26147](https://redhat.atlassian.net/browse/ARO-26147)).

IcM `runbook_url` points at
[aka.ms/arohcp-runbook-cluster-monitoring](https://aka.ms/arohcp-runbook-cluster-monitoring).

Registered only in `observability/alerts-rp-hcps.yaml` (not DEV) so the Azure
`prometheusRuleGroup` name does not collide with the DEV HCP module's action
groups.

All four alerts ship at severity `"4"` until SAW alert-tester confirms series
presence and firing rates against prod/int `hcps-*` datasources.

## Coverage

| Alert | Signal | HCP identity |
|---|---|---|
| `UJClusterMonitoringOperatorUnavailable` | `cluster_operator_conditions{name="monitoring", condition="available"} == 0` | `namespace` |
| `UJClusterMonitoringOperatorDegraded` | `cluster_operator_conditions{name="monitoring", condition="degraded"} == 1` | `namespace` |
| `UJAlertmanagerMainNotReady` | `kube_statefulset_*{statefulset="alertmanager-main", namespace="openshift-monitoring"}` | `hostedcontrolplane` |
| `UJGuestPrometheusOperatorNotReady` | `prometheus_operator_ready{namespace="openshift-monitoring"}` | `hostedcontrolplane` |

Guest Alertmanager / prometheus-operator series keep `namespace=openshift-monitoring`
and carry the HCP id on `hostedcontrolplane` (see HCP remote_write keep rules in
`observability/prometheus/deploy/templates/prometheus.yaml`). Worker gates join
via `label_replace` of `node_collector_zone_size` namespace → `hostedcontrolplane`
so one HCP's workers cannot gate another.

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
