# Kubernetes Provisioning Diagnostics

## Overview

This dashboard provides visibility into Kubernetes-level resource issues that can impact HCP cluster provisioning performance. It tracks pod health, container restarts, node resource pressure, and scheduling delays across management clusters.

**Dashboard:** `HCP Kubernetes Provisioning Diagnostics` (in Grafana under "Performance & Scale" folder)

**Related Jira:** [ARO-26753](https://redhat.atlassian.net/browse/ARO-26753)

## Context

During burst cluster creation (15+ clusters), provisioning times can breach the 20-minute SLO due to resource saturation on management clusters. This dashboard helps diagnose:

- **Pod failures** - Crashes, OOMKills, or image pull failures
- **Scheduling delays** - Pending pods due to resource constraints
- **Container restarts** - Crash loops indicating stability issues
- **Node pressure** - Memory, disk, or PID exhaustion on management cluster nodes

## Dashboard Sections

### 1. Pod Health by Namespace

**Panels:**
- **Running Pods per Namespace** - Running pods over time
- **Failed Pods per Namespace** - Failed pods indicating crashes or errors
- **Pending Pods per Namespace** - Pods waiting to be scheduled
- **Pod Phase Distribution** - Stacked view of all pod states

**What to look for:**
- Sudden drops in running pods during burst provisioning
- Spikes in failed or pending pods correlating with slow cluster creation
- Sustained high pending pod counts indicating resource exhaustion

### 2. Pod Restart Analysis

**Panels:**
- **Container Restarts per Namespace** - Restart rate over 5-minute windows
- **Top 10 Restarting Pods** - Specific pods with the most restarts in the last hour

**What to look for:**
- High restart rates (>0.1/sec) indicating crash loops
- Restarts in critical namespaces (hypershift, OCM, maestro)
- Specific pods repeatedly restarting during provisioning windows

### 3. Node Resource Pressure

**Panels:**
- **Nodes Under Memory Pressure** - Count of nodes evicting pods due to memory
- **Nodes Under Disk Pressure** - Count of nodes with disk space issues
- **Nodes Under PID Pressure** - Count of nodes with process ID exhaustion
- **Node Pressure Timeline** - Historical view of pressure conditions

**What to look for:**
- Any nodes showing pressure (count > 0) during burst provisioning
- Correlation between node pressure and slow cluster creation times
- Repeated pressure events indicating undersized nodes or resource leaks

### 4. Scheduling and Resource Correlation

**Panels:**
- **Unschedulable Pods** - Pods that cannot be scheduled due to constraints
- **Pod Distribution Heatmap** - Pod distribution across nodes and namespaces

**What to look for:**
- Unschedulable pods > 0 indicates resource exhaustion
- Uneven pod distribution suggesting scheduling affinity issues
- Hot spots on specific nodes during burst creation

## Example Queries

### Show Failed Pods During Provisioning Burst

```promql
# Failed pods by namespace
sum(kube_pod_status_phase{cluster="cspr-westus3-mgmt-1", phase="Failed"}) by (namespace)
```

**Use case:** Set time range to the provisioning burst window (e.g., "April 23, 08:38-09:04") to see which namespaces had pod failures.

### Which Namespaces Had the Most Restarts?

```promql
# Top 5 namespaces by restart rate over last hour
topk(5, sum(rate(kube_pod_container_status_restarts_total{cluster="cspr-westus3-mgmt-1"}[1h])) by (namespace))
```

**Use case:** Identify namespaces with stability issues during or after burst provisioning.

### Are Any Nodes Under Pressure During Provisioning?

```promql
# Memory pressure by node
sum(kube_node_status_condition{cluster="cspr-westus3-mgmt-1", condition="MemoryPressure", status="true"}) by (node)

# Disk pressure by node
sum(kube_node_status_condition{cluster="cspr-westus3-mgmt-1", condition="DiskPressure", status="true"}) by (node)

# PID pressure by node
sum(kube_node_status_condition{cluster="cspr-westus3-mgmt-1", condition="PIDPressure", status="true"}) by (node)
```

**Use case:** Check if management cluster nodes are resource-saturated during slow provisioning.

### Unschedulable Pods Indicating Resource Exhaustion

```promql
# Count of unschedulable pods
sum(kube_pod_status_unschedulable{cluster="cspr-westus3-mgmt-1"}) by (namespace, pod)
```

**Use case:** Zero unschedulable pods = healthy. Any value > 0 indicates the cluster cannot schedule new pods.

### Pod Distribution to Identify Hotspots

```promql
# Pods per node by namespace
count(kube_pod_info{cluster="cspr-westus3-mgmt-1"}) by (node, namespace)
```

**Use case:** Check if certain nodes are overloaded during burst creation.

## Correlating with Cluster Provisioning Events

To diagnose slow provisioning, cross-reference this dashboard with cluster service logs:

1. **Identify the time window** - Find the cluster creation timestamps from cluster-service logs
   - Example: April 23, 08:38 (cluster moves to Installing) → 09:04 (cluster Ready) = 26 min

2. **Set the dashboard time range** - Use the Grafana time picker to focus on that window

3. **Check each section:**
   - **Pod Health:** Were there failed/pending pods in `ocm-*` or `hypershift` namespaces?
   - **Restarts:** Were critical pods (maestro-agent, hypershift-operator) restarting?
   - **Node Pressure:** Were management cluster nodes under memory/disk/PID pressure?
   - **Scheduling:** Were pods unschedulable due to resource constraints?

4. **Compare to baseline** - Check the same metrics during normal (non-burst) provisioning to identify anomalies

## Troubleshooting Scenarios

### Scenario 1: Cluster Provisioning Takes >20 Minutes

**Symptoms:**
- Cluster moves to Installing state quickly
- HostedCluster CR created in management cluster
- Long delay before cluster reaches Ready state

**Diagnosis Steps:**
1. Check **Pending Pods per Namespace** - Are HCP control plane pods pending?
2. Check **Node Pressure** - Is the management cluster resource-saturated?
3. Check **Unschedulable Pods** - Can new pods even be scheduled?
4. Check **Pod Distribution Heatmap** - Are nodes unevenly loaded?

**Common Causes:**
- Management cluster nodes undersized for burst load
- Too many HCPs on one management cluster (need more shards)
- Resource requests set too high on HCP components

### Scenario 2: Pods Keep Restarting During Provisioning

**Symptoms:**
- Cluster eventually provisions but takes long time
- Intermittent failures in provisioning flow

**Diagnosis Steps:**
1. Check **Top 10 Restarting Pods** - Which pods are crash-looping?
2. Check **Container Restarts per Namespace** - Which namespace has the issue?
3. Check **Node Pressure** - Are restarts due to OOMKills (memory pressure)?

**Common Causes:**
- Memory limits too low on HCP components (OOMKilled)
- Liveness probe configured too aggressively
- External dependency issues (maestro MQTT, postgres)

### Scenario 3: Failed Pods Block Provisioning

**Symptoms:**
- Provisioning gets stuck
- Cluster never reaches Ready state

**Diagnosis Steps:**
1. Check **Failed Pods per Namespace** - Which pods failed?
2. Check specific pod logs: `kubectl logs -n <namespace> <pod>`
3. Check pod events: `kubectl describe pod -n <namespace> <pod>`

**Common Causes:**
- Image pull failures (registry issues, wrong image tag)
- Init container failures (configuration errors)
- Insufficient RBAC permissions

## Accessing the Dashboard

### From Grafana UI
1. Log into Grafana for your target environment (dev/cspr/int/stg/prod)
2. Navigate to **Dashboards** → **Performance & Scale** folder
3. Select **HCP Kubernetes Provisioning Diagnostics**

### Direct Link
```
https://<grafana-url>/d/hcp-k8s-provisioning-diagnostics
```

### Variables
- **Datasource** - Select the Prometheus datasource for your environment
- **Cluster** - Select the management cluster (e.g., `cspr-westus3-mgmt-1`)
- **Namespace** - Filter to specific namespaces or use "All"
  - Common namespaces to monitor:
    - `ocm-*` - Open Cluster Management (HCP orchestration)
    - `hypershift` - HCP control plane operator
    - `maestro` - Event distribution
    - `clusters-*` - Per-HCP cluster namespaces

## Related Dashboards

- **Cluster Service SLO** - API-level provisioning metrics and SLO tracking
- **Maestro Server** - Event delivery metrics (MQTT, postgres, gRPC)
- **AKS Performance** - Management cluster infrastructure metrics

## Metrics Reference

All metrics are sourced from `kube-state-metrics` running in the management cluster:

| Metric | Description | Labels |
|--------|-------------|--------|
| `kube_pod_status_phase` | Pod lifecycle phase (Running, Pending, Failed, etc.) | namespace, pod, uid, phase |
| `kube_pod_container_status_restarts_total` | Container restart count | namespace, pod, uid, container |
| `kube_node_status_condition` | Node condition states (MemoryPressure, DiskPressure, etc.) | node, condition, status |
| `kube_pod_status_unschedulable` | Pods that cannot be scheduled | namespace, pod, uid |
| `kube_pod_info` | Pod metadata for pod counts | namespace, pod, node, uid |

## See Also

- [ARO-26753: Add Kubernetes metrics](https://redhat.atlassian.net/browse/ARO-26753)
- [ARO-26043: Investigate provisioning duration SLO breach](https://redhat.atlassian.net/browse/ARO-26043)
- [ARO-26893: Observability metrics epic](https://redhat.atlassian.net/browse/ARO-26893)
- [Cleanup Stuck Cluster Deletion](./cleanup-stuck-cluster-deletion.md)
- [HCP Cluster Creation Flow](./hcp-cluster-creation-flow.md)
