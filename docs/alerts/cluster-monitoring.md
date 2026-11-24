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

Alerts ship at severity `"4"` until SAW alert-tester confirms series presence
and firing rates against prod/int `hcps-*` datasources.

## Coverage

| Alert | Signal | HCP identity |
|---|---|---|
| `UJClusterMonitoringOperatorUnavailable` | `cluster_operator_conditions{name="monitoring", condition="available"} == 0` | `namespace` |
| `UJClusterMonitoringOperatorDegraded` | `cluster_operator_conditions{name="monitoring", condition="degraded"} == 1` | `namespace` |

## Deferred (no guest metrics yet)

`UJAlertmanagerMainNotReady` and `UJGuestPrometheusOperatorNotReady` need
`kube_statefulset_*` / `prometheus_operator_ready` from guest
`openshift-monitoring`. The per-HCP collector in
`mgmt-agent/pkg/controller/ksmhcp/resources.go` currently allowlists only node
and ingress-controller metrics, so those series are not in AMW. Revisit after
ksmhcp collection is expanded (or another scrape path lands).

## SAW alert-tester (metric presence first)

```bash
# From alert-tester checkout, b-account + VPN as needed:
./atest grafana --grafana-url "$GRAFANA_URL" --datasource hcps-uksouth \
  -q 'count(cluster_operator_conditions{name="monitoring"})' \
  --from 2026-11-17 --to 2026-11-24
```
