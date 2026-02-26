# Logging

## Overview

ARO-HCP uses Azure Data Explorer (Kusto) for centralized log aggregation. Logs from service/management AKS clusters and Hosted Control Planes are collected via Fluent Bit and forwarded to Kusto clusters for long-term storage and querying.

## Architecture

### Geographies

Kusto clusters are group by Geography, according to the Geos defined in the Ev2 configuration: https://github.com/Azure/ARO-Tools/blob/main/pkg/config/ev2config/config.yaml

Names follow this convention:
 - rg: "hcp-kusto-{{ .ctx.environment }}-{{ .ev2.geoShortId
 - kustoName: "hcp-{{ .ctx.environment }}-{{ .ev2.geoShortId }}"

*instance list*: https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/components-and-architecture/kusto

### Dual Database Design

ARO-HCP uses two databases to separate service logs from Hosted Control Plane logs:

- **ServiceLogs**: Frontend, backend, clusters-service logs, general container logs (non-OCM namespaces), and Kubernetes events
- **HostedControlPlaneLogs**: Container logs and Kubernetes events from OCM namespaces (pattern: `ocm-*`)

This separation ensures data isolation between platform infrastructure and customer workloads.

### Kusto Clusters

Kusto clusters are provisioned per environment and region via Bicep templates with:

- **Retention**: 14 days soft delete, 2 days hot cache
- **Autoscaling**: Configurable min/max nodes (via `enableAutoScale` parameter)
- **SKU**: Environment-specific (e.g., `Standard_D12_v2` for production, `Dev(No SLA)_Standard_D11_v2` for development)

**Note**: The database was originally named `customerLogs` but renamed to `HostedControlPlaneLogs`. Clusters support cross-tenant access (e.g., AME tenant) when configured with appropriate permissions.

## Table Schemas

Table schemas are defined in KQL files under [`dev-infrastructure/modules/logs/kusto/tables/`](../../dev-infrastructure/modules/logs/kusto/tables/).

### ServiceLogs Database

1. **`frontendLogs`** ([schema](../../dev-infrastructure/modules/logs/kusto/tables/frontendLogs.kql)): Frontend service logs with HTTP request/response details and tracking IDs
2. **`backendLogs`** ([schema](../../dev-infrastructure/modules/logs/kusto/tables/backendLogs.kql)): Backend service logs with operation tracking and error codes
3. **`clustersServiceLogs`** ([schema](../../dev-infrastructure/modules/logs/kusto/tables/clustersServiceLogs.kql)): Clusters-service logs with cluster resource IDs
4. **`containerLogs`** ([schema](../../dev-infrastructure/modules/logs/kusto/tables/containerLogs.kql)): General container logs from non-OCM namespaces
5. **`kubernetesEvents`** ([schema](../../dev-infrastructure/modules/logs/kusto/tables/kubernetesEvents.kql)): Kubernetes events from all clusters

### HostedControlPlaneLogs Database

1. **`containerLogs`** ([schema](../../dev-infrastructure/modules/logs/kusto/tables/containerLogs.kql)): Container logs from OCM namespaces (`ocm-*` pattern)
2. **`kubernetesEvents`** ([schema](../../dev-infrastructure/modules/logs/kusto/tables/kubernetesEvents.kql)): Kubernetes events from Hosted Control Planes

All tables include common columns: `timestamp`, `log` (dynamic JSON), `environment`, `cluster`, `region`, and Kubernetes metadata (`namespace_name`, `container_name`, `pod_name`, `host`).

## Data Ingestion

### Fluent Bit Forwarder

Fluent Bit runs as a DaemonSet (`arobit-forwarder`) on each AKS cluster, collecting logs and forwarding them to Kusto:

- **Inputs**: Container logs from `/var/log/containers/*.log`, forward protocol (port 24224), Fluent Bit metrics
- **Filters**: CRI reassembly, metadata enrichment (`environment`, `region`, `cluster`), Kubernetes metadata, namespace/container routing
- **Outputs**: Azure Kusto output plugin using workload identity authentication

### Authentication

Fluent Bit authenticates using **Azure Workload Identity** (MSI client ID, token from `AZURE_FEDERATED_TOKEN_FILE`). Explicit ingestion endpoints are configured per cluster via Helm values.

### Log Routing

| Source | Namespace | Container Pattern | Database | Table |
|--------|-----------|-------------------|----------|-------|
| Frontend | Non-OCM | `aro-hcp-frontend*` | ServiceLogs | `frontendLogs` |
| Backend | Non-OCM | `aro-hcp-backend*` | ServiceLogs | `backendLogs` |
| Clusters Service | Non-OCM | `clusters-service*` | ServiceLogs | `clustersServiceLogs` |
| Kubernetes Events | Any | `kube-events*` | ServiceLogs | `kubernetesEvents` |
| General Containers | Non-OCM | Other | ServiceLogs | `containerLogs` |
| HCP Containers | `ocm-*` | Any | HostedControlPlaneLogs | `containerLogs` |
| HCP Events | `ocm-*` | `kube-events*` | HostedControlPlaneLogs | `kubernetesEvents` |

**Management clusters** route OCM namespace logs to `HostedControlPlaneLogs`; **service clusters** route service logs to `ServiceLogs` with table-specific routing.

## Querying Logs

### hcpctl must-gather

The `hcpctl must-gather` command queries and exports logs from Kusto:

**Query Command** (recommended for dev/int clusters):

```bash
hcpctl must-gather query \
  --kusto hcp-dev-us \
  --region westus3 \
  --subscription-id <subscription-id> \
  --resource-group <resource-group> \
  --output-path ./must-gather-output
```

**Common Options**: `--kusto`, `--region`, `--subscription-id`, `--resource-group` (all required), `--output-path` (default: auto-generated), `--query-timeout` (default: 5m, range: 30s-30m), `--skip-hcp-logs`, `--timestamp-min`/`--timestamp-max`, `--limit`

**Output Structure**:

```text
<output-path>/
├── service/
│   ├── containerLogs.json
│   ├── frontendContainerLogs.json
│   ├── backendContainerLogs.json
│   └── clustersServiceLogs.json
├── host-control-plane/
│   └── containerLogs.json
└── options.json
```

### Direct KQL Queries

Query Kusto directly using Azure Data Explorer or the Kusto client library (`tooling/hcpctl/pkg/kusto/`):

```kql
clustersServiceLogs
| where timestamp >= datetime("2024-01-01T00:00:00Z") and timestamp <= datetime("2024-01-02T00:00:00Z")
| where resource_id has "subscription-id" and resource_id has "resource-group"
| project timestamp, log, cluster, namespace_name, container_name
| order by timestamp asc
| limit 1000
```

**Best Practices**: Always specify time ranges, filter on `resource_id`/`subscription_id`/`cluster_id`, use `limit`, filter on indexed fields (`timestamp`, `cluster`, `namespace_name`), and use `project` to select needed columns.

Follow up read: https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/kustoqueries

## Infrastructure

Kusto infrastructure is defined in `dev-infrastructure/modules/logs/kusto/` and deployed via `dev-infrastructure/kusto-pipeline.yaml`:

- **main.bicep**: Orchestrates cluster, databases, tables, and permissions
- **cluster.bicep**: Cluster resource definition
- **database.bicep**: Database creation with retention policies
- **script.bicep**: KQL script execution for table creation
- **database-users.bicep**: Permission management
- **grant-access.bicep**: Access grant utilities

Configuration is in `config/config.yaml`.

## Monitoring and Verification

E2E tests verify log ingestion (`test/e2e/kusto_logs_present.go`, `test/util/verifiers/kusto.go`). The verifier checks for logs from:
- `aro-hcp` namespace: `aro-hcp-frontend`, `aro-hcp-backend` containers
- `clusters-service` namespace: `clusters-service-server` container
- `ocm-*` namespaces: `kube-apiserver` container

Automated cleanup of stale role assignments is available in `test/util/cleanup/kusto-role-assignments/`.

## References

- [Must-Gather Commands Documentation](sops/gather-logs.md)
- [Monitoring Documentation](monitoring.md)
- [Azure Data Explorer Documentation](https://learn.microsoft.com/en-us/azure/data-explorer/)
- [Fluent Bit Azure Kusto Output Plugin](https://docs.fluentbit.io/manual/pipeline/outputs/azure-kusto)
