# Monitoring

## Overview

ARO-HCP uses a combination Azure Managed Prometheus agents and self-managed Prometheus to monitor both the service/management AKS clusters and the Hosted Control Planes. Metrics are collected via Prometheus Server and remote written to regional Azure Monitor Workspaces. A global instance of Azure Managed Grafana references every Azure Monitor Workspace in the cloud environment as a data source.

## Prometheus Stack

### Azure Managed Prometheus

Azure Managed Prometheus is enabled through the `aks-cluster-base.bicep` module via the `azureMonitorProfile.metrics.enabled: true` setting. This automatically provisions Azure Monitor agents on AKS nodes and enables comprehensive infrastructure monitoring.

**Azure Managed Prometheus Configuration:**
- Configured via `ama-metrics-settings-configmap` in the `kube-system` namespace
- **Scrape Interval**: 30 seconds for all targets  
- **Built-in Targets Enabled**: kubelet, coredns, cadvisor, kubeproxy, apiserver, nodeexporter, control plane components (etcd, scheduler, controller-manager), and network observability (Retina, Hubble, Cilium)
- **Disabled Targets**: kube-state-metrics (handled by self-managed Prometheus), Windows exporters
- **Metadata Collection**: Supports custom `metricLabelsAllowlist` and `metricAnnotationsAllowList` via Bicep parameters

Azure Managed Prometheus handles **cluster-level infrastructure metrics** and automatically forwards them to the regional Azure Monitor Workspace associated with each AKS cluster.

### Self-Managed Prometheus Stack

A self-managed Prometheus stack is deployed to service and management AKS clusters using the community-maintained [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) Helm chart. This Prometheus instance handles **application and service metrics** from both service/management clusters and hosted control planes.

**Configuration:**
- Helm chart customized via `observability/prometheus/values.yaml` (trimmed-down version of upstream)
- **Replicas and shards** configurable via cluster configuration in `config/config.yaml`
- **Global Discovery**: Monitors all namespaces (`serviceMonitorNamespaceSelector: {}`, `podMonitorNamespaceSelector: {}`)
- **Workload Identity**: Uses Microsoft Entra Workload Identity with "Monitoring Metrics Publisher" role on DCRs

**Dual Remote Write Architecture:**
Self-managed Prometheus implements namespace-based routing to two Azure Monitor Workspaces:

1. **Service Monitoring Workspace** (`prometheusSpec.remoteWriteUrl`):
   - Receives metrics from **all namespaces except** those matching `^ocm-<environment>.*`
   - Handles infrastructure services, applications, and general cluster metrics

2. **HCP Monitoring Workspace** (`prometheusSpec.hcpRemoteWriteUrl`):
   - Receives metrics **only from** namespaces matching `^ocm-<environment>.*`  
   - Handles Hosted Control Plane specific metrics (OCM-related components)

**Deployment:**
The Prometheus stack is deployed via `dev-infrastructure/mgmt-pipeline.yaml` and `dev-infrastructure/svc-pipeline.yaml` pipelines.

## Application Metrics Collection

Application metrics are collected through Kubernetes custom resources that define scraping targets for the self-managed Prometheus stack.

### ServiceMonitor and PodMonitor Resources

Each service deployed to AKS clusters includes either a `ServiceMonitor` or `PodMonitor` resource in its Helm chart. The Prometheus stack automatically discovers these resources across **all namespaces** via global selectors.

**Scrape Interval**: 30 seconds (most services)

## Hosted Control Plane Metrics

Hosted Control Plane (HCP) metrics are scraped by the same Prometheus server that scrapes services on the management cluster.

To enable this, the `prometheus` namespace in the **management cluster** includes an additional label (`network.openshift.io/policy-group=monitoring`). This label is required to allow traffic through the network policy that governs Prometheus scrape access to the Hosted Control Plane namespaces.

Each **Hosted Control Plane** will have multiple `ServiceMonitor` and `PodMonitor` resources for core control plane components such as **etcd**, **kube-apiserver**, and others.  These monitors define how Prometheus should scrape metrics from each component, including details like the endpoint, port, and **TLS configuration**.  TLS settings in the monitors reference Kubernetes **Secrets** stored in the **hosted cluster namespace**. These secrets contain the certificates required to establish secure connections to the metrics endpoints.  The Prometheus server, running in the **management cluster**, has access to these secrets and uses them to configure TLS connections when scraping the Hosted Control Plane component metrics.

## Metrics Infrastructure

### Dual Workspace Architecture

ARO-HCP implements two Azure Monitor Workspace to separate metrics based on their source and purpose:

**1. Service Monitoring Workspace (Primary)**
- **Scope**: Infrastructure services, applications, and general cluster metrics
- **Sources**: Azure Managed Prometheus (infrastructure) + Self-managed Prometheus (applications)
- **Namespace Filter**: All namespaces **except** `ocm-<environment>.*`
- **Data Flow**: 
  - Azure Managed Prometheus → Direct ingestion
  - Self-managed Prometheus → Remote write with namespace filtering

**2. HCP Monitoring Workspace (Hosted Control Planes)**
- **Scope**: Hosted Control Plane specific metrics
- **Sources**: Self-managed Prometheus only
- **Namespace Filter**: **Only** namespaces matching `ocm-<environment>.*`
- **Data Flow**: Self-managed Prometheus → Remote write with namespace filtering

This separation ensures clean metric isolation between platform infrastructure/services and customer data.

### Global Grafana

A single **Azure Managed Grafana** instance is deployed globally and configured with data sources for **both workspace types** in each region. This provides:
- Unified visualization across all services and Hosted Control Planes
- Region-agnostic dashboard experience
- Consolidated alerting and monitoring workflows

### Regional Azure Monitor Workspace

Each region contains **two Azure Monitor Workspaces (AMW)**:
1. **Service AMW**: Receives infrastructure and application metrics
2. **HCP AMW**: Receives Hosted Control Plane metrics

Metrics are ingested via associated **Data Collection Rules (DCR)** and **Data Collection Endpoints (DCE)** for each AKS cluster.

### Alerting

Prometheus metrics written to Azure Monitor Workspaces can be queried using PromQL. Alert rules are defined directly within an Azure Monitor Workspace, and when triggered they generate incidents in **IcM** (Internal Case Management system).

### Per-Cluster Data Collection Rule (DCR)

Each AKS cluster has its own **Data Collection Rule** that defines:

- **Source**: Typically a **DCE**, where Prometheus writes the metrics.
- **Destination**: The **Azure Monitor Workspace** that stores the metrics.
- **Routing rules**: Optional rules to filter or route metrics based on labels (e.g., sending certain metrics to specific AMWs based on cluster or workload metadata).

### Per-Cluster Data Collection Endpoint (DCE)

A **Data Collection Endpoint** provides a set of Azure-hosted endpoints that accept telemetry data (metrics, logs, traces). In ARO-HCP:

- Only **metrics** are sent to the DCE.
- The **metrics ingestion endpoint** on the DCE acts as the **remote write target** for the Prometheus server running in the AKS cluster.

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

### 2. Log Analytics Workspace (Configured via Diagnostic Settings)

For detailed logs and historical analysis, Azure Front Door diagnostic settings export data to Log Analytics workspace:

**Available Data:**
- **FrontDoorAccessLog**: Detailed request/response data (URL, status code, client IP, user agent, cache status, etc.)
- **FrontDoorHealthProbeLog**: Backend health probe results
- **FrontDoorWebApplicationFirewallLog**: WAF rule matches, blocks, and actions
- **AllMetrics**: Same metrics as Azure Monitor, stored in Log Analytics for longer retention

**Configuration:**
Diagnostic settings are automatically deployed when Log Analytics is enabled in the environment configuration:

```yaml
# config/config.yaml
logs:
  loganalytics:
    enable: true
```

This creates diagnostic settings via `dev-infrastructure/modules/oidc/afd-datacollection.bicep` that export to the regional Log Analytics workspace.

**How to Query in Grafana:**
1. Add **Azure Monitor Logs** (Log Analytics) as a data source in Grafana
2. Create a new dashboard panel
3. Select the Log Analytics data source
4. Write KQL queries against AFD tables:

```kusto
// Request count by status code
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| summarize count() by httpStatusCode_d, bin(TimeGenerated, 5m)

// Top requested URLs
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| summarize RequestCount = count() by requestUri_s
| top 10 by RequestCount

// WAF blocks over time
AzureDiagnostics
| where Category == "FrontDoorWebApplicationFirewallLog"
| where action_s == "Block"
| summarize count() by bin(TimeGenerated, 5m)

// Backend health status
AzureDiagnostics
| where Category == "FrontDoorHealthProbeLog"
| summarize by healthProbeId_s, httpStatusCode_d, TimeGenerated
```

**Advantages:**
- Detailed request-level data
- Custom KQL queries for complex analysis
- Longer retention (configurable, up to 730 days)
- Correlation with other Azure service logs
- WAF security insights

**Limitations:**
- Requires Log Analytics workspace (additional cost)
- Slight ingestion delay (typically < 1 minute)

### Recommended Approach

Use **both methods** for comprehensive monitoring:
- **Azure Monitor metrics**: Real-time dashboards for operational monitoring (request rate, latency, errors)
- **Log Analytics**: Deep-dive analysis, troubleshooting, security investigations, and historical trends

### Environments with AFD Monitoring Enabled

Log Analytics (and thus AFD diagnostic settings) is enabled in:
- `dev` - Integrated development environment
- `cspr` - Cluster service PR check environment
- `pers` - Personal development environments (when configured)

To enable in other environments (`ntly`, `perf`, `swft`), add to `config/config.yaml`:
```yaml
environments:
  <environment-name>:
    defaults:
      logs:
        loganalytics:
          enable: true
```
