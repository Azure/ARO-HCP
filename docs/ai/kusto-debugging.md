# Agentic Hints for Debugging ARO HCP With Kusto

- IMPORTANT: this document is referenced by agentic workflows, DO NOT REMOVE IT.
- If any of the info below turns out not to be accurate, suggest to the user an update PR at the end of the session.

## Clusters
- the `hcp-prod-usc` kusto cluster is US canary region and matches prod grafana's `*-eastus2euap` data sources

## See Also

For curated, ready-to-adapt KQL queries that trace an ARM request through
every layer of the stack (frontend → backend → CS → Maestro → HyperShift),
see [query-cookbook.md](query-cookbook.md). The cookbook also documents
the discovery chain for resolving correlation IDs into the magic strings
(CS internal cluster `cid`, Maestro bundle IDs) that downstream queries need.

## Databases

- `HostedControlPlaneLogs` – logs from hosted control plane
- `ServiceLogs` — logs from service and management clusters (underlay)

## Cross-cluster queries (prod)

- Prod Kusto clusters are split by geography, so a single resource's logs live on exactly one cluster. To query every prod cluster at once, use the environment-scoped entity groups (provisioned on all prod clusters): `AllServiceLogs` spans the `ServiceLogs` database, `AllHostedControlPlaneLogs` spans `HostedControlPlaneLogs`, and `AllMonitoringEvents` spans `MonitoringEvents`.
- Entity groups are database scoped. Select the matching database before running the query. For example, select `MonitoringEvents` before using `AllMonitoringEvents`. The group will not resolve, or appear in `.show entity_groups`, from another database.
- `macro-expand <group> as X ( X.<table> | ... )` runs the wrapped query on every member cluster and unions the result; project `X.$current_cluster_endpoint` to tag each row with its source cluster.
- Use this for discovery when you do not know the home cluster, for example resolving a `cid` from a resource ID fleet-wide:
  ```kql
  macro-expand AllServiceLogs as X
  (
      X.clustersServiceLogs
      | where resource_id has '<sub-rg-id>'
      | distinct cid, SourceCluster = X.$current_cluster_endpoint
  )
  ```
- Prefer a single cluster for heavy log pulls: a resource lives on one cluster, so fanning a large export across all members only adds cost and hits per-cluster query limits. Use the groups for discovery and fleet-wide questions, then run the heavy query against the resolved cluster. The groups are environment scoped and include only clusters tagged for the selected environment.

## Tables

- `containerLogs` — generic container logs from all pods; used for maestro, hypershift, and any service without a dedicated table
- `kubernetesEvents` — Kubernetes API Event objects (warnings, scheduling/mount/probe failures, `reason`/`message` on
  involved objects). Ingested by `kube-events` on service and management clusters into **`ServiceLogs.kubernetesEvents`**
  only. Filter `eventNamespace`, `objectKind`, `objectName`, `cluster`. Not the same as mgmt-agent `pod event` container logs.
- `frontendLogs` — ARM API request/response logs: HTTP method, path, status code, duration, request/correlation IDs, subscription/resource identifiers
- `backendLogs` — backend service logs: async operations, controller state dumps, cluster lifecycle, error codes, correlation IDs
- `clustersServiceLogs` — Cluster Service logs: cluster provisioning phases, operation IDs, cluster correlation IDs
- `fleetLogs` — fleet controller logs: fleet reconciliation events, fleet data dumps
- `aksEvents` / `kubeAudit` — AKS management cluster events and extracted K8s API audit logs: API verbs, request URIs, user agents, response status
- `systemdLogs` — node-level systemd unit logs: hostname, systemd unit, message

## Common Query Patterns

- All tables have `timestamp`, `environment`, `cluster`, `region` columns for scoping.
- Time filtering: `| where timestamp between (startTime .. endTime)`
- Service-specific tables add structured `log` (dynamic) with fields like `log.msg`, `log.content`.
- `frontendLogs` is the entry point for tracing a request (use `client_request_id` or `correlation_request_id`).
- `backendLogs` links to `frontendLogs` via `correlation_request_id` and contains state dumps from controllers.
- **Kubernetes events**: `database('ServiceLogs').table('kubernetesEvents')` — scope `cluster`, `eventNamespace`,
  `objectName`, `reason`. Snapshot `controlPlaneEvents` / `hypershift/events` use this table. See
  [kubernetes-events.md](../../tooling/hcpctl/pkg/agent/prompts/exemplars/kubernetes-events.md).
- `containerLogs` is filtered by `namespace_name` and `container_name` to isolate maestro/hypershift logs.
- **mgmt-agent** (`container_name == 'mgmt-agent-controller'`, Service database, management `cluster`):
  `log.msg == 'resource event'` — CR add/update/delete with full `log.object` (Hypershift, ACM, CAPI,
  `multitenancy.acn.azure.com`, …). `log.msg == 'pod event'` — pod lifecycle with full Pod in `log.object`.
  Write ad-hoc scoped queries; do not expect these in the snapshot dump. See
  [mgmt-agent-event-logs.md](../../tooling/hcpctl/pkg/agent/prompts/exemplars/mgmt-agent-event-logs.md).
- Maestro bundle tracking joins `containerLogs` + `backendLogs`/`clustersServiceLogs` via bundle ID extraction.
- Hosted cluster namespaces on management clusters take the form `ocm-arohcp<env>-<cid>-<id>`, where `cid` is the Clusters Service internal cluster ID (an opaque hash like `2iig1flm0pfjr9h8kkg6ggbjig1p3fpa`). Use `| distinct pod_name, container_name` within such a namespace to enumerate available pods/containers.
- Use `| where namespace_name !contains 'ocm-arohcp'` against `database('HostedControlPlaneLogs').table('containerLogs')` to review management-cluster-level components.
- The Kusto ingest mappings and table schemas live in [dev-infrastructure/modules/logs/kusto/tables/](../../dev-infrastructure/modules/logs/kusto/tables/) — consult them when a query needs a column you haven't seen used before.
