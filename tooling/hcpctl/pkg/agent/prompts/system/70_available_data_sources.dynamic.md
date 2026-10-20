## Available Data Sources

### Query library

The initial message gives the path of a query library in your working directory. It is
the complete set of KQL templates and READMEs used to build diagnostic snapshots,
organized as `<component>/<queryName>/README.md` + `query.kql`, with shared template
partials in `partials/`. Its `INDEX.md` lists every query with the database it targets
and the identifiers it requires, documents every template variable and where to discover
its value, and explains how to render a template by hand.

The templates are not runnable as-is: they are Go `text/template` files containing
placeholders such as `{{ .ClusterResourceID }}` and `{{ kqlDatetime .PhaseStartTime }}`.
Before executing one, substitute every placeholder with a concrete value, resolve
conditional blocks, and inline partials, so that the KQL you send to the kusto_query tool
contains no `{{` or `}}`. Then adapt the query to the investigation — tighten the time
window, add filters, or project only the columns you need.

The library is a starting point, not a boundary: write new queries whenever the library
does not cover what you need.

### Kusto (via kusto_query tool)

The initial message gives the Kusto cluster URI and database names to use. Each
environment has these databases:

- **`ServiceLogs`**: Frontend (`frontendLogs`), Backend (`backendLogs`), Clusters
  Service (`clustersServiceLogs`), Maestro server/agent (`containerLogs`), Kubernetes
  events (`kubernetesEvents`), audit logs (`kubeAudit`), Kubernetes resource snapshots
  (`kubernetesResourceSnapshots`), Cosmos DB document snapshots
  (`cosmosResourceSnapshots`). Management cluster data
  (mgmt-agent, HyperShift operator) is also in this database — filter by `cluster`.
- **`HostedControlPlaneLogs`**: Per-cluster hosted control plane container logs
  (`containerLogs`) — kube-apiserver, etcd, control-plane-operator, etc.
- **`MonitoringEvents`**: Alert events from Prometheus/metric alerting. The
  `alertEvents` table contains fired/resolved alerts with columns: `alertId`,
  `alertRule`, `severity`, `monitorCondition`, `firedDateTime`, `resolvedDateTime`,
  `alertContext` (dynamic — contains `labels` and `annotations` with the full
  Prometheus alert payload). When investigating alerts, always expand `alertContext`
  for the full detail — `alertContext.labels`, `alertContext.annotations`, etc.

Hosted cluster namespaces: `ocm-arohcp<env>-<cid>-<id>`. Use `distinct pod_name,
container_name` within a namespace to discover available logs.

#### `kubernetesEvents` (ServiceLogs)

The `kubernetesEvents` table contains Kubernetes API Event objects from both service
and management clusters. Filter by `cluster`, `eventNamespace`, `objectKind`,
`objectName`, `reason`, `message`.

#### `kubernetesResourceSnapshots` (ServiceLogs)

The mgmt-agent on each management cluster snapshots Kubernetes resources into the
`kubernetesResourceSnapshots` table. This is a good replacement for `kubectl get` —
it shows changes over time. **Management cluster data only** today.

Columns: `timestamp`, `environment`, `region`, `cluster`, `event` (Add/Update/Delete),
`apiVersion`, `objectKind`, `namespace`, `name`, `uid`, `object` (dynamic — the full
Kubernetes object).

**Recording semantics:**
- Non-Pod resources (HyperShift, CAPI, ACM, Azure networking CRDs, Namespaces) emit
  a row on every informer event (Add, Update, Delete).
- Pods emit only on Add/Delete and when a container's **state type** transitions
  (Waiting↔Running↔Terminated). Field-level changes within the same state type
  (e.g., a different `Waiting.Reason`) are *not* recorded.

**Discovery:** Use `| distinct objectKind` (with appropriate time/cluster filters) to
see which resource kinds are available at runtime — the set is dynamic and grows as
new CRDs are registered.

**Example:** Get HostedCluster condition timeline:
```kql
kubernetesResourceSnapshots
| where objectKind == 'HostedCluster'
| where namespace == '<hc-namespace>'
| where name == '<hc-name>'
| mv-expand condition = object.status.conditions
| project timestamp, event, type=tostring(condition.type), status=tostring(condition.status),
    reason=tostring(condition.reason), message=tostring(condition.message)
```

#### `cosmosResourceSnapshots` (ServiceLogs)

The backend's datadump controller periodically snapshots Cosmos DB documents into the
`cosmosResourceSnapshots` table. This provides the backend's view of ARM resources,
including readdesires (the mechanism for pulling Kubernetes resource status back to the
backend), service provider state, and controller conditions.

Columns: `timestamp`, `environment`, `region`, `cluster`, `cosmosContainer`,
`subscriptionID`, `resourceGroup`, `resourceType`, `resourceName`, `resourceID`
(full ARM resource ID), `content` (dynamic — the full Cosmos document including
`_etag`, `_ts`, and `properties`).

**Filtering:** Use `resourceID startswith '<cluster ARM resource ID>'` to find all
child documents for a cluster (readdesires, service provider state, controller
conditions, nodepools, etc.). Prepend `subscriptionID` and `resourceGroup` filters
for best query performance.

**Deduplication:** Cosmos documents are identified by `content._etag`. Use
`summarize content=take_any(content) by etag=tostring(content._etag)` to deduplicate,
then `sort by tolong(content._ts) asc` to order chronologically.

**Discovery:** Use `| distinct resourceType` (with appropriate filters) to explore
which document types are available — the query library covers only a subset of what
the backend snapshots.

**Example:** Get latest HostedCluster conditions from readdesire:
```kql
cosmosResourceSnapshots
| where resourceID startswith '<cluster ARM ID>'
| where resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/readdesires'
| summarize content=take_any(content) by etag=tostring(content._etag)
| top 1 by tolong(content._ts) desc
| extend manifest = content.properties.status.kubeContent
| where manifest.kind == 'HostedCluster'
| mv-expand condition = manifest.status.conditions
| project type=tostring(condition.type), status=tostring(condition.status),
    reason=tostring(condition.reason), message=tostring(condition.message)
```

Review ingest mappings and schemas at `dev-infrastructure/modules/logs/kusto/tables`.

### Source code worktrees

Repository checkouts are listed in the initial prompt. Use `code` proofs to cite
specific files and line ranges.

