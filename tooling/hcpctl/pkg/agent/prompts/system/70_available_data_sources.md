## Available Data Sources

### Pre-gathered data directory

The data directory contains pre-canned Kusto query results organized by phase and
resource. Review the manifest's `directory_layout` for the full structure. Key locations:

- `test_logs/{error,output}.log` — test stderr and stdout
- `node_boot_logs/<node>-console.log` — VM serial console output (present when
  `manifest.node_console_logs` is populated). Node names follow the pattern
  `<cluster>-<nodepool>-<random>`. When machines are stuck in `WaitingForNodeRef`,
  `ignitionNotReached`, or `OSProvisioningTimedOut`, **always check these first** —
  they show the node's view of boot (ignition TLS errors, kubelet failures, CNI
  issues) that the orchestrator-side Kusto queries cannot reveal.
- `azure_sdk_log/azure.log` — the client's own record of every Azure SDK call the
  test makes, across all resource providers (ARO-HCP RP, Managed Identity, Key
  Vault, Network, etc.): each request, response, error, retry, and LRO poll, with
  correlation IDs and timing. Present when `manifest.azure_log` is set. It is the
  only client-side view of these calls, so consult it for any failure that turns on
  how the client issued or handled an Azure request — for example (not limited to):
  retries behind a conflict/`DeploymentActive`/already-exists error; transient
  5xx/429 with backoff; LRO polling problems such as a 404 while a create
  propagates or a poll that times out; and client-side timeouts, cancellations, or
  transport/auth errors.
- `discovery/` — resource IDs, cluster associations, request mappings
- `<phase>/resources/<type>/<name>/` — state, conditions, logs, events per resource
- `<phase>/events/` — service-level Kubernetes events

### Kusto (via kusto_query tool)

Each environment has two databases:

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
which document types are available — the examples in gathered data show only a subset
of what the backend snapshots.

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

