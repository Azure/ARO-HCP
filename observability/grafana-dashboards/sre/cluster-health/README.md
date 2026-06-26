# SRE Cluster Health Dashboards

Grafana dashboards for ARO-HCP SRE oncall and fleet health monitoring.

## Dashboards

| Dashboard | Purpose |
|---|---|
| `resource-state.json` | Fleet-wide cluster provisioning state, clusters per region, nodepool health |
| `operations-overview.json` | Fleet-wide in-flight operations, stuck/failed ops, duration distribution |
| `per-cluster-drill-in.json` | Single-cluster oncall triage: KAS, etcd, provisioning state, nodepools |

## Datasource Model

Each region has two Azure Monitor Workspaces exposed as Prometheus datasources:

- **Services AMW** (`Managed_Prometheus_services-<region>`) — metrics from all namespaces except `ocm-<env>.*`. Used for backend provisioning state, operations, and nodepool metrics.
- **HCPs AMW** (`Managed_Prometheus_hcps-<region>`) — metrics from `ocm-<env>.*` HCP namespaces only. Used for KAS, etcd, and control-plane component signals.

### Datasource Variable Conventions

Fleet-wide dashboards use `-- Mixed --` datasource with a `$datasource` variable for fan-out across all regions.

Per-cluster dashboards use two datasource variables:
- `$ds_services` — selects a single Services AMW (env+region)
- `$ds_hcps` — selects a single HCPs AMW (same env+region)

### Datasource Regex

Regex patterns exclude obsolete datasources that end with 2-3 letter shortcodes (e.g. `am`, `bn`, `cbn`, `ln`, `yt`):

```
^Managed_Prometheus_services-(?![a-z]{2,3}$).+$
^Managed_Prometheus_hcps-(?![a-z]{2,3}$).+$
```

Valid datasources end with full Azure region names (4+ chars), so this pattern safely excludes all legacy short suffixes. See [docs/ai/grafana-debugging.md](../../../../docs/ai/grafana-debugging.md) for details.

## Extending

1. Add or edit dashboard JSON in this folder.
2. The folder is registered in [`observability/observability.yaml`](../../../observability.yaml) as `SRE Cluster Health`.
3. The `GrafanaDashboards` EV2 step deploys all JSON files in this folder to the `SRE Cluster Health` Grafana folder in INT, STG, and PROD.
4. No manual upload is needed — merge to `main` and the pipeline handles deployment.

## Kusto / ADX Integration (Planned)

Regional Kusto clusters host `ServiceLogs` and `HostedControlPlaneLogs` databases. Error log volume panels from these sources are planned but not yet implemented — no existing dashboards in this repo use ADX datasources today.
