# SWIFT Networking: Dashboard Design (ARO-25920)

## File location

```
observability/grafana-dashboards/sre/user-journey/swift-networking.json
```

Register in `observability/observability.yaml`. The pipeline creates the "SRE User Journey" folder in Grafana and imports the dashboard.

## Data source

Datasource variable regex: `^.*-mgmt-\d+$` (management cluster Prometheus)

SWIFT is management-cluster-only. All panels use this datasource. The dashboard should include a datasource variable so SREs can switch between management clusters.

> **Future:** Once [ARO-HCP#4878](https://github.com/Azure/ARO-HCP/pull/4878) lands, Kusto (Azure Data Explorer) will be available as a Grafana datasource for historical analysis and capacity planning panels.

## Starter reference

The `azure-container-networking` repo ships a starter Grafana dashboard at [`cns/doc/examples/metrics/grafana.json`](https://github.com/Azure/azure-container-networking/blob/master/cns/doc/examples/metrics/grafana.json) covering per-node IP utilization. Use as a base for IP pool state panels.

## ADR-001 Baseline Coverage Requirements

Every dashboard must include panels for all five baseline metrics:

| Baseline metric | Panel |
|---|---|
| Availability | Router pod startup p99 vs SLO threshold |
| Errors | CNS IP assignment error rate |
| Latency | CNS IP assignment p99 |
| Traffic | CNS `requestipconfig` call rate |
| Saturation | SWIFT NIC utilization per node |

## Panel Design Standards (ADR-001)

- **SLO target line** on every SLI panel: current value always shown against the objective
- **Color thresholds** (green/amber/red) as the metric approaches or breaches the SLO
- **Error budget panel:** remaining budget for the current measurement window
- **Alerts annotation:** `ALERTS{alertname=~"userJourneySwift.*|SwiftCNS.*|SwiftPending.*", alertstate="firing"}` overlaid on all panels so pre/post-alert context is visible without switching tools

## Dashboard Structure

### Section 1: Journey Health

**Panel: Router pod startup p99**
- Query: `router:startup_latency:p99_avg_1h` (or overlay all four window averages)
- All four window averages overlaid: `p99_avg_5m`, `p99_avg_30m`, `p99_avg_1h`, `p99_avg_6h`
- Threshold reference line at 300s
- Rationale: shows burn rate development. Fast window spiking before slow window = developing incident

**Panel: Raw per-pod ContainerCreating duration**
- Query: `router:startup_latency:seconds` (per-pod detail)
- Useful for seeing which specific pods are stuck and for how long

**Panel: CNS daemonset availability**
- Query:
  ```promql
  kube_daemonset_status_number_ready{daemonset="azure-cns", namespace="kube-system"}
  /
  kube_daemonset_status_desired_number_scheduled{daemonset="azure-cns", namespace="kube-system"}
  ```
- SLO target line at 99.9%

**Panel: SWIFT NIC utilization per node**
- Query:
  ```promql
  sum by (node) (kube_pod_container_resource_requests{resource="aro.openshift.io/swift-nic"})
  /
  kube_node_status_capacity{resource="aro.openshift.io/swift-nic"}
  ```
- Capacity planning; no SLO threshold (scale-out is automatic)

**Panel: IPs stuck in PendingProgramming**
- Query: `cx_pending_programming_ips_v2`
- Threshold alert line at 0: any sustained non-zero is the primary proxy for stuck MTPNCs
### Section 2: Component Health

**Panel: IP assignment error rate**
- Query:
  ```promql
  sum(rate(http_request_latency_seconds_count{url=~".*/requestipconfig.*", cns_return_code!="0"}[5m]))
  /
  sum(rate(http_request_latency_seconds_count{url=~".*/requestipconfig.*"}[5m]))
  ```
- SLO threshold at 1%

**Panel: IP assignment latency p99**
- Query: `histogram_quantile(0.99, sum(rate(ip_assignment_latency_seconds_bucket[5m])) by (le))`
- SLO threshold at 10s

**Panel: `requestipconfig` traffic rate**
- Query: `sum(rate(http_request_latency_seconds_count{url=~".*/requestipconfig.*"}[5m]))`
- No SLO; use for anomaly detection (sustained zero with nodes Ready = something stopped scheduling CX pods)

**Panel: IP pool state per node**
- Queries: `cx_assigned_ips_v2`, `cx_available_ips_v2`, `cx_allocated_ips_v2` stacked
- From starter dashboard: `cns/doc/examples/metrics/grafana.json`

**Panel: IP pool saturation**
- Query: `sum by (instance) (cx_assigned_ips_v2) / sum by (instance) (cx_ipam_max_ips)`
- Threshold at 85% for saturation signal

**Panel: Subnet exhaustion**
- Query: `cx_ipam_subnet_exhaustion_state > 0`
- Binary state panel

**Panel: IP pool convergence**
- Query: `cx_ipam_requested_ips - cx_ipam_total_ips`
- Healthy: slightly negative (~-1, DNC pre-allocates a buffer). Alert on **positive** values: means CNS requested IPs that DNC hasn't delivered.

## Authoring Reference

- [How to create an ARO HCP Dashboard](https://docs.google.com/document/d/1Va9ksYqO2nvhk2d4vjrLu5pNuO-A2I2gnMDohlc9dk4/edit?tab=t.3usr5sfulcj3)
- [docs/grafana-dashboards.md](https://github.com/Azure/ARO-HCP/blob/main/docs/grafana-dashboards.md)
- [ARO HCP Alerting Recommendation ADR](https://github.com/openshift-online/architecture/pull/79): dashboard design requirements
- [Monitoring / Metrics / Alerting Recommendation for ARO HCP](https://docs.google.com/document/d/11WCoJa7E8X9dalgzwtnCiPeRHD72AkoqMLG71gK61OA/edit)
