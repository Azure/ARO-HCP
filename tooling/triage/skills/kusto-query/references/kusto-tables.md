# Kusto Tables

## Cross-Database Queries

When config points to HostedControlPlaneLogs, query ServiceLogs tables with:
  database('ServiceLogs').kubernetesEvents
  database('ServiceLogs').containerLogs
Direct ServiceLogs.tableName syntax does NOT work.

## ServiceLogs.kubernetesEvents

Columns: timestamp (datetime), eventNamespace (string), objectKind (string), objectName (string), reason (string), message (string)

Common reasons: Scheduled (message contains node name), Pulled, Created, Started, Unhealthy (probe failed), Killing (liveness failed), BackOff, FailedMount

## ServiceLogs.containerLogs

Columns: timestamp (datetime), namespace_name (string), pod_name (string), container_name (string), log (string)

Note: namespace_name uses underscores, kubernetesEvents uses camelCase eventNamespace.
Global scans of containerLogs are expensive and may time out — always scope by namespace_name or use startswith.
Not all namespaces have container logs.

## HCP Namespace Prefixes

ocm-arohcpci01- — CI e2e test runs
ocm-arohcppers- — personal dev clusters
ocm-arohcpcspr- — cluster service PR check
ocm-arohcpint- — integration
ocm-arohcpstg- — stage
ocm-arohcpprod- — production
ocm-zgalor- — zgalor personal dev (legacy)
