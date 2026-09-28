# Monitoring

## Overview

ARO-HCP collects Prometheus metrics from the service (SVC) and management (MGMT) AKS clusters and from the Hosted Control Planes (HCPs), and ingests them into regional **Azure Monitor Workspaces (AMW)**. A single global **Azure Managed Grafana** instance references every AMW in the cloud environment as a data source.

Each region has **two AMWs**:

- **Service AMW** — infrastructure and application/service metrics.
- **HCP AMW** — Hosted Control Plane metrics (OCM namespaces).

Metrics are routed to the correct AMW by a per-sample label, `microsoft_metrics_include_label` (`service` or `hcp`); see [Metric routing](#metric-routing).

## Collection modes

How metrics are scraped is controlled by a single config lever, **`monitoringApiGroup`**, set per cluster type (`svc` / `mgmt`) in `config/config.yaml` and the cloud overlays:

| `monitoringApiGroup`      | Mode        | Scraper                                              |
| ------------------------- | ----------- | --------------------------------------------------- |
| `azmonitoring.coreos.com` | **AMA**     | Azure Monitor managed Prometheus (AKS add-on)       |
| `monitoring.coreos.com`   | **Legacy**  | Self-managed (OSS) `kube-prometheus-stack` Prometheus |

The value is the Kubernetes API group used for `ServiceMonitor`/`PodMonitor` CRDs. Every service's monitor template renders its `apiVersion` from this lever (`{{ .Values.monitoringApiGroup }}/v1`), so the same chart targets either the AMA operator or the OSS operator without change.

**Current rollout:** AMA in all **dev** environments and **int**; **stg** and **prod** remain on legacy OSS Prometheus until AMA is validated there.

Azure Monitor managed Prometheus itself is enabled on every cluster via `azureMonitorProfile.metrics.enabled: true` in `aks-cluster-base.bicep`; the `monitoringApiGroup` lever only decides whether AMA or OSS Prometheus does the application scraping.

## AMA mode

### SVC clusters (pure AMA)

- `kube-prometheus-stack` deploys **CRDs only** — the OSS operator, bundled kube-state-metrics, and default alerting rules are disabled. No OSS Prometheus server runs.
- Services emit their `ServiceMonitor`/`PodMonitor` directly in the `azmonitoring.coreos.com` group, which the AMA-managed operator discovers.
- AMA scrapes service metrics and kube-state-metrics (its built-in KSM is enabled in AMA mode) into the **Service AMW**.

### MGMT clusters (hybrid CRD layout)

MGMT clusters host both our services and the HCPs, so both API groups are in play:

- The OSS prometheus-operator and its CRDs **stay deployed** — HyperShift emits HCP component monitors in the `monitoring.coreos.com` group — but, as on SVC, **no OSS Prometheus server runs**. AMA does all scraping.
- Our own services emit monitors directly in `azmonitoring.coreos.com` (via `monitoringApiGroup`), discovered by AMA → **Service AMW**.
- Two mgmt-agent controllers bridge HCP metrics into AMA:
  - **`monitortranslator`** copies `monitoring.coreos.com` `ServiceMonitor`/`PodMonitor` resources in OCM namespaces into `azmonitoring.coreos.com` equivalents so AMA can discover them. It injects a metric-relabel rule stamping `microsoft_metrics_include_label: hcp` so the samples route to the **HCP AMW**. It skips the per-HCP KSM monitor, which `ksmhcp` already creates directly in the target group.
  - **`amanetpolicy`** creates a `NetworkPolicy` (`ama-metrics-allow`) in each HCP namespace permitting AMA metrics pods (in `kube-system`) to scrape the HCP Prometheus endpoints.

### AMA configuration

AMA settings are deployed by the **`service-lifecycle`** chart (to `kube-system`):

- `ama-metrics-settings-configmap` — scrape targets and intervals (30s). `kubestate` is enabled only in AMA mode.
- `ama-metrics-prometheus-config` — a custom scrape job that defaults `microsoft_metrics_include_label` to `service` when unset.
- `ama-metrics-configmap-reader` — RBAC letting the AMA service account read these ConfigMaps.

### HostedCluster custom-resource metrics

AMA's built-in kube-state-metrics cannot produce metrics from `HostedCluster` custom resources (the SLI/SLA availability signals). A standalone **`kube-state-metrics`** chart (`observability/kube-state-metrics/`, deployed to the `ksm-crs` namespace on MGMT clusters) fills this gap via a custom-resource-state config that emits `hostedClusterAPI_*` series. Per-HCP worker-node KSM metrics are still handled by the `ksmhcp` controller; see [`mgmt-agent/pkg/controller/ksmhcp/README.md`](../mgmt-agent/pkg/controller/ksmhcp/README.md).

## Legacy OSS mode (stg/prod)

Where `monitoringApiGroup` is `monitoring.coreos.com`, a self-managed `kube-prometheus-stack` Prometheus (`observability/prometheus/`) scrapes all application and HCP metrics and remote-writes them to the AMWs:

- Monitors are discovered cluster-wide (`serviceMonitorNamespaceSelector: {}`).
- Namespace-based routing: samples from `ocm-<environment>.*` namespaces are kept for the HCP remote-write and tagged `microsoft_metrics_include_label: hcp`; everything else goes to the Service AMW.
- Workload Identity with the "Monitoring Metrics Publisher" role authenticates remote-write to the per-cluster DCE.

The MGMT `prometheus` namespace carries the `network.openshift.io/policy-group=monitoring` label so network policy permits scraping HCP namespaces, and HCP component monitors reference TLS secrets in the hosted-cluster namespace that the management-cluster Prometheus can read.

### Migration cleanup

Once a cluster has moved to AMA, the `*.CleanupPrometheus` pipelines (`dev-infrastructure/{svc,mgmt}-cleanup-prometheus.pipeline.yaml`, run via `scripts/cleanup-prometheus.sh`) remove the OSS prometheus namespace and cluster-scoped resources. The script no-ops unless `MONITORING_API_GROUP=azmonitoring.coreos.com`, and on MGMT clusters it preserves the CRDs (still needed by HyperShift and the translator).

## Metric routing

Regardless of mode, every sample is tagged with `microsoft_metrics_include_label` (`service` or `hcp`), and routing to the two AMWs is done by the **Data Collection Rules (DCR)**:

- Each AKS cluster has a **Service DCR** (`labelIncludeFilter: microsoft_metrics_include_label=service` → Service AMW) and, where an HCP AMW exists, an **HCP DCR** (`labelIncludeFilter: ...=hcp` → HCP AMW), each wired to the cluster with its own **Data Collection Rule Association (DCRA)**.
- Metrics reach the DCRs through a per-cluster **Data Collection Endpoint (DCE)**, which is the metrics-ingestion (remote-write) target.

This label-filtered, dual-DCR design keeps platform/service metrics and customer HCP metrics cleanly isolated.

## Global Grafana

A single **Azure Managed Grafana** instance is deployed globally with data sources for both AMW types in every region, providing unified, region-agnostic dashboards and alerting across services and HCPs.

> **Note:** In staging, integration, and production, public network access to Grafana may be disabled, requiring the **MSFT Corp VPN**. Dev Grafana is publicly accessible. See [Grafana VPN Access](sops/grafana-vpn-access.md).

### ADX integration fabrics

The global Grafana reconciliation can optionally manage Dashboard resource provider integration-fabric children for ARO-HCP Kusto clusters. It discovers clusters in the Grafana subscription tagged with `aroHCPPurpose=logs`, the current `aroHCPEnvironment`, and `aroHCPGeoShortId`.

The Kusto access grant and integration-fabric reconciliation have separate disabled-by-default gates:

- `monitoring.adxKustoAccessEnabled` grants the Grafana workspace system identity database-level `Viewer` access to `ServiceLogs` during a geography rollout.
- `monitoring.adxIntegrationEnabled` enables integration-fabric reconciliation during a global rollout.
- `monitoring.adxIntegrationGeographies` is the required complete comma-separated geography short ID set when integration reconciliation is enabled. Matching is case-insensitive and ignores surrounding whitespace.

The integration-fabric resource provider contract still needs nonproduction validation. `monitoring.adxIntegrationScenario` and `monitoring.adxIntegrationTargetResourceId` are therefore explicit configuration inputs rather than hardcoded assumptions. Do not enable the feature until the accepted scenario, target semantics, resulting Grafana datasource configuration, and resource provider ownership behavior have been confirmed.

An empty scenario or target leaves that optional property unmanaged. Clearing a previously configured value does not remove the value already returned by the resource provider.

Before enabling Kusto access in an existing environment, list the `ServiceLogs` database principal assignments and confirm that the Grafana principal does not already have a `Viewer` assignment under another resource name. Kusto rejects duplicate role and principal assignments even when their resource names differ.

Use this rollout order:

1. Deploy the Kusto discovery tags while both gates remain disabled.
2. Set `monitoring.adxKustoAccessEnabled` and run the geography pipelines for the complete target geography set.
3. Validate the resource provider contract in nonproduction, including scenario, target, datasource behavior, and integration-fabric tag round-trip.
4. Build the complete geography allowlist from the geography pipelines' `ev2.geoShortId` values, then run `grafanactl manage reconcile` manually with the normal Grafana arguments, the ADX integration arguments, and `--dry-run`. Confirm every expected geography is selected and the planned operations contain no unexpected `delete` entries.
5. Set the complete geography allowlist and validated resource provider inputs, enable `monitoring.adxIntegrationEnabled`, and run the global pipeline.

Narrowing the geography allowlist removes owned integration fabrics that are no longer selected. Disabling integration reconciliation leaves fabrics unchanged, and Kusto principal assignments are deployed incrementally. Rollback therefore requires explicit removal of both the integration fabrics and the Viewer assignment.

## Alerting

Prometheus metrics in the AMWs are queried with PromQL. Alert rules are defined directly in the AMW (see `dev-infrastructure/modules/metrics/rules/`); when triggered they raise incidents in **IcM**. A per-cluster `underlay_clusters{cluster=...,cluster_type=...}` series, emitted at deploy time by `underlay-clusters-metric.bicep`, provides the authoritative SVC/MGMT cluster inventory that alerts scope against.

## Azure Front Door Monitoring

Azure Front Door metrics and logs are available in Grafana through two complementary approaches:

### 1. Direct Azure Monitor Metrics (No Configuration Required)

Azure Front Door automatically publishes platform metrics to Azure Monitor. These metrics are immediately available in Grafana without any additional configuration:

**Available Metrics:**
- Request count and rate
- Latency (backend, total)
- Cache hit ratio
- Error rates (4xx, 5xx)
- Backend health percentage
- Web Application Firewall (WAF) request count

**How to Query in Grafana:**
1. Add **Azure Monitor** as a data source in Grafana (typically pre-configured)
2. Create a new dashboard panel
3. Select Azure Monitor data source
4. Choose:
   - **Subscription**: Your Azure subscription
   - **Resource Group**: `global`
   - **Resource Type**: `Microsoft.Cdn/profiles`
   - **Resource**: Your Front Door profile name (e.g., `arohcpdev`)
   - **Metric Namespace**: `Microsoft.Cdn/profiles`
   - **Metric**: Select from available metrics (e.g., `RequestCount`, `TotalLatency`, `Percentage4XX`)

**Advantages:**
- Zero configuration required
- Real-time metrics
- Standard Azure Monitor aggregations (avg, min, max, sum, count)

**Limitations:**
- Metrics only (no detailed logs)
- Limited retention (90 days by default)
- No custom KQL queries
