# Monitoring

## Overview

ARO-HCP uses a self-managed Prometheus stack to monitor both the service/management AKS clusters and the Hosted Control Planes. Metrics are collected via Prometheus Server and remote written to regional Azure Monitor Workspaces. A global instance of Azure Managed Grafana references every Azure Monitor Workspace in the cloud environment as a data source.

## Prometheus Stack

A Prometheus stack is deployed to the service and management AKS clusters using the community-maintained [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) Helm chart.  The Helm chart is customized using a modified version of the upstream `values.yaml`, located at `observability/prometheus/values.yaml`. This file is a trimmed-down version of the original. Refer to the upstream `values.yaml` for additional configurable settings. Deployment customization is further handled through configuration files and by dynamically fetching Azure deployment outputs at deploy time.

The number of **Prometheus replicas and shards** is configurable via the svc/mgmt cluster's configuration in `config/config.yaml`. This allows tuning based on cluster size, expected metrics volume, and HA requirements.

Prometheus uses **remote write** to persist metrics to Azure Monitor Workspaces. The Prometheus server runs an authentication proxy sidecar configured for [Microsoft Entra Workload Identity](https://learn.microsoft.com/en-us/azure/azure-monitor/containers/prometheus-remote-write-azure-workload-identity#set-up-remote-write-with-microsoft-entra-workload-id). The `prometheus` identity is assigned the "Monitoring Metrics Publisher" role on the Data Collection Rule (DCR) associated with each AKS cluster.

The prometheus stack is deployed to service and management clusters apart of the `dev-infrastructure/mgmt-pipeline.yaml` and `dev-infrastructure/svc-pipeline.yaml`.  There is a default `pipeline.yaml` at `observability/prometheus/pipeline.yaml` for testing purposes, it will only deploy to the management cluster.

## Application Metrics Collection

Each service deployed to the AKS clusters includes either a `ServiceMonitor` or a `PodMonitor` resource in its Helm chart.  The one exception to this is the hypershift operator, the hypershift operator lays down its own service monitor.  These resources define how Prometheus should scrape metrics from the service or pods.  The Prometheus stack is configured to discover `ServiceMonitor` and `PodMonitor` resources across **all namespaces**.

## Hosted Control Plane Metrics

Hosted Control Plane (HCP) metrics are scraped by the same Prometheus server that scrapes services on the management cluster.

To enable this, the `prometheus` namespace in the **management cluster** includes an additional label (`network.openshift.io/policy-group=monitoring`). This label is required to allow traffic through the network policy that governs Prometheus scrape access to the Hosted Control Plane namespaces.

Each **Hosted Control Plane** will have multiple `ServiceMonitor` and `PodMonitor` resources for core control plane components such as **etcd**, **kube-apiserver**, and others.  These monitors define how Prometheus should scrape metrics from each component, including details like the endpoint, port, and **TLS configuration**.  TLS settings in the monitors reference Kubernetes **Secrets** stored in the **hosted cluster namespace**. These secrets contain the certificates required to establish secure connections to the metrics endpoints.  The Prometheus server, running in the **management cluster**, has access to these secrets and uses them to configure TLS connections when scraping the Hosted Control Plane component metrics.

## Metrics Infrastructure

### Global Grafana

A single **Azure Managed Grafana** instance is deployed globally and configured with a data source for each **Azure Monitor Workspace** (AMW) in the environment. This allows users to visualize metrics from all services and Hosted Control Planes in a centralized dashboard experience, regardless of region or cluster.

### Regional Azure Monitor Workspace

Each region contains an **Azure Monitor Workspace (AMW)** that receives metrics from clusters in that region. Metrics are ingested from each cluster via its associated **Data Collection Rule (DCR)** and **Data Collection Endpoint (DCE)**.

There is currently one AMW per region for **services** and **HCPs**.

### Alerting

Prometheus metrics written to Azure Monitor Workspaces can be queried using PromQL. Alert rules are defined directly within an Azure Monitor Workspace, and when triggered they generate incidents in **IcM** (Internal Case Management system).

Currently, AMWs are integrated with IcM via an **IcM Connector**, which routes fired alerts to appropriate incident queues. The IcM Connector is a legacy mechanism and will be migrated to **IcM Actions**.

### Per-Cluster Data Collection Rule (DCR)

Each AKS cluster has its own **Data Collection Rule** that defines:

- **Source**: Typically a **DCE**, where Prometheus writes the metrics.
- **Destination**: The **Azure Monitor Workspace** that stores the metrics.
- **Routing rules**: Optional rules to filter or route metrics based on labels (e.g., sending certain metrics to specific AMWs based on cluster or workload metadata).

### Per-Cluster Data Collection Endpoint (DCE)

A **Data Collection Endpoint** provides a set of Azure-hosted endpoints that accept telemetry data (metrics, logs, traces). In ARO-HCP:

- Only **metrics** are sent to the DCE.
- The **metrics ingestion endpoint** on the DCE acts as the **remote write target** for the Prometheus server running in the AKS cluster.

This setup enables secure and scalable ingestion of Prometheus metrics into Azure Monitor with native integration via workload identity.