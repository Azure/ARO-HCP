# Logging

## Overview

ARO-HCP uses Azure Data Explorer (Kusto) for centralized log aggregation. Logs from service/management AKS clusters and Hosted Control Planes are collected via Fluent Bit (the `arobit` forwarder) and forwarded to Kusto for long-term storage and querying.

Routing and per-table transformation are performed by **ADX update policies** inside a single database, **`AllAroHcpLogs`**. Fluent Bit's only job is to enrich records and ship them to one raw landing table; everything else happens in KQL.

> **Status.** The `AllAroHcpLogs` architecture described here is being rolled out on
> the `rewrite-kusto-routing` branch. The implementation plan, phased rollout, and
> testing strategy live in [`logging-allarohcplogs-spec.md`](logging-allarohcplogs-spec.md).
>
> **⚠️ Deprecation.** The previous **dual-database** design — the `ServiceLogs` and
> `HostedControlPlaneLogs` databases with routing done in the Fluent Bit forwarder —
> is **deprecated**. Those databases **will be deleted** once all readers are
> repointed at `AllAroHcpLogs`. See [Deprecated: legacy dual-database setup](#deprecated-legacy-dual-database-setup).
> Do not add new tables, mappings, or readers to the legacy databases.

## Architecture

### Geographies

Kusto clusters are grouped by Geography, according to the Geos defined in the Ev2 configuration: https://github.com/Azure/ARO-Tools/blob/main/config/ev2config/config.yaml

Names follow this convention:
 - rg: "hcp-kusto-{{ .ctx.environment }}-{{ .ev2.geoShortId }}"
 - kustoName: "hcp-{{ .ctx.environment }}-{{ .ev2.geoShortId }}"

*instance list*: https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/components-and-architecture/kusto

### Single-database design (`AllAroHcpLogs`)

All logs land in **one** database, `AllAroHcpLogs`. Because update policies cannot
cross databases, keeping everything in one database lets KQL own the *entire*
routing decision — including the Hosted-Control-Plane vs. service split that the
forwarder used to make.

- **Database default retention**: 90 days soft delete, 2 days hot cache.
- **HCP logs** (namespace prefix `ocm-*`) route to a dedicated **`hcpContainerLogs`**
  table with a **table-level** retention of **14 days**, preserving the isolation
  and shorter retention the old `HostedControlPlaneLogs` database provided — but as
  a property of the table, not a separate database.
- Data isolation between platform infrastructure and customer (HCP) workloads is
  preserved by routing `ocm-*` logs into their own table.

The cluster is provisioned per environment and region via Bicep with configurable
autoscaling (`enableAutoScale`) and an environment-specific SKU (`kusto.sku`).
Clusters support cross-tenant access (e.g. AME tenant) when configured with the
appropriate permissions.

### Update-policy routing

```
containers / journald / azure-vnet
        │
        ▼
  Fluent Bit (arobit DaemonSet)
    ├─ enrich: environment/region/cluster, k8s metadata,
    │           cluster_id + resource_group from HCP namespace annotation (mgmt)
    └─ ONE azure_kusto output, ONE ingestion mapping:
          everything → AllAroHcpLogs.rawLogs   (ephemeral)

  ADX database: AllAroHcpLogs   (default softdelete = 90d)
    rawLogs (timestamp, tag, log: dynamic)   softdelete = 0s   ← single funnel
        │  update policies (transactional), each a 1-function KQL transform:
        │
        ├─ hcpContainerLogs   ← namespace startswith "ocm-"       [retention 14d]
        ├─ frontendLogs       ← container_name startswith "aro-hcp-frontend"
        ├─ backendLogs        ← container_name startswith "aro-hcp-backend"
        ├─ clustersServiceLogs← container_name startswith "clusters-service"
        ├─ fleetLogs          ← container_name startswith "fleet-controller"
        ├─ kubeApplierLogs    ← container_name startswith "kube-applier"
        ├─ kubernetesEvents   ← container_name startswith "kube-events"
        ├─ cosmosResourceSnapshots     ← snapshotType == "cosmos"
        ├─ kubernetesResourceSnapshots ← snapshotType == "kubernetes"
        ├─ systemdLogs        ← tag startswith "systemd"
        ├─ azureVnetLogs      ← tag == "azure-vnet.logs"
        └─ containerLogs      ← catch-all: non-ocm, non-service, non-snapshot
```

This is the same pattern already used for the Event-Hub-fed tables (`alertEvents`,
`aksEvents`/`kubeAudit`, `maestroMqttConnections`): a raw landing table → a
transform function → a **transactional** update policy → a typed table.

Benefits over the deprecated forwarder-routing design:

- The forwarder shrinks from ~13 `azure_kusto` outputs (each with its own on-disk
  buffer) and ~7 `rewrite_tag`/`grep`/`modify` routing filters down to **one
  output and zero routing rules**.
- Routing lives in KQL and is validated in CI by the Kusto emulator (`make verify-kql`).
- Adding a new table is a **single `.kql` change** — no forwarder edit and no Helm
  fixture regeneration.

## Table Schemas

Table schemas are defined in KQL files under
[`dev-infrastructure/modules/logs/kusto/tables/allarohcplogs/`](../dev-infrastructure/modules/logs/kusto/tables/allarohcplogs/).
Each target table's `.kql` file contains three parts: the `.create-merge table`
(schema), a `.create-or-alter function rawToX()` (the transform that replaces the
old ingestion mapping — the per-column `$.log.*` JSON paths become `project`
lines), and a `.alter table X policy update` binding the function to `rawLogs`.

### `AllAroHcpLogs` database

| Table | Source filter | Notes |
|-------|---------------|-------|
| `rawLogs` | — (ingestion target) | Ephemeral landing table, `softdelete = 0s`, single `rawMapping`. Not queried directly. |
| `hcpContainerLogs` | `namespace_name startswith "ocm-"` | HCP container logs. **`softdelete = 14d`.** |
| `frontendLogs` | `container_name startswith "aro-hcp-frontend"` | HTTP request/response details and tracking IDs. |
| `backendLogs` | `container_name startswith "aro-hcp-backend"` | Operation tracking, controller name, error codes. |
| `kubeApplierLogs` | `container_name startswith "kube-applier"` | Schema-compatible with `backendLogs` so queries port between them. |
| `clustersServiceLogs` | `container_name startswith "clusters-service"` | Clusters-service logs with cluster resource IDs. |
| `fleetLogs` | `container_name startswith "fleet-controller"` | Controller name, resource identifiers, resource type. |
| `kubernetesEvents` | `container_name startswith "kube-events"` | Kubernetes events. |
| `cosmosResourceSnapshots` | `snapshotType == "cosmos"` | Cosmos resource snapshots (emitted by frontend/backend/fleet). |
| `kubernetesResourceSnapshots` | `snapshotType == "kubernetes"` | Kubernetes resource snapshots. |
| `systemdLogs` | `tag startswith "systemd"` | Host systemd/journald logs. |
| `azureVnetLogs` | `tag == "azure-vnet.logs"` | Azure VNet CNI host-file logs (see [Azure VNet CNI Logs](#azure-vnet-cni-logs)). |
| `containerLogs` | catch-all (non-ocm, non-service, non-snapshot) | General container logs that match no specific table. |

Container-log tables include `timestamp`, `log` (dynamic JSON), `environment`,
`cluster`, `region`, and Kubernetes metadata. `hcpContainerLogs` additionally
carries `cluster_id` and `resource_group` (from the HCP namespace annotation).
Host-file logs (`azureVnetLogs`, `systemdLogs`) use `hostname` for the node and do
not have collector-Pod metadata.

> Target table schemas (columns, order, types) are **identical** to the old
> same-named tables, except for the new `hcpContainerLogs`. That is deliberate:
> readers migrate from the legacy databases to `AllAroHcpLogs` by a configuration
> repoint, not a query rewrite.

### Routing source of truth

The container-name prefixes are defined once, in a helper function used by both the
per-service (positive) filters and the catch-all (negative) filter:

```kusto
.create-or-alter function with (skipvalidation = "true")
KnownServiceContainerPrefixes() {
    datatable(prefix: string)
    [
        "aro-hcp-frontend", "aro-hcp-backend", "clusters-service",
        "fleet-controller", "kube-applier", "kube-events",
    ]
}
```

Adding a new service table = add its prefix here + add one `.kql` file. Nothing in
Fluent Bit changes, and each log still lands in exactly one table.

### Example `.kql`

Raw landing table:

```kusto
// tables/allarohcplogs/00_rawLogs.kql
.create-merge table rawLogs (timestamp: datetime, tag: string, log: dynamic)
.alter-merge table rawLogs policy retention softdelete = 0s recoverability = disabled
.create-or-alter table rawLogs ingestion json mapping 'rawMapping'
```
[
  {"column":"timestamp","Properties":{"path":"$.timestamp"}},
  {"column":"tag","Properties":{"path":"$.tag"}},
  {"column":"log","Properties":{"path":"$.log"}}
]
```
```

Dedicated HCP table with 14-day retention:

```kusto
// tables/allarohcplogs/hcpContainerLogs.kql
.create-merge table hcpContainerLogs (
  timestamp: datetime, log: dynamic, environment: string, cluster: string,
  namespace_name: string, container_name: string, container_image: string,
  host: string, labels: string, pod_name: string, region: string, tag: string,
  container_status_image_id: string, cluster_id: string, resource_group: string
)
.alter-merge table hcpContainerLogs policy retention softdelete = 14d

.create-or-alter function with (skipvalidation = "true")
rawToHcpContainerLogs() {
    rawLogs
    | where tostring(log.kubernetes.namespace_name) startswith "ocm-"
    | project
        timestamp,
        log = log.log,
        environment = tostring(log.environment),
        cluster = tostring(log.cluster),
        namespace_name = tostring(log.kubernetes.namespace_name),
        container_name = tostring(log.kubernetes.container_name),
        container_image = tostring(log.kubernetes.container_image),
        host = tostring(log.kubernetes.host),
        labels = tostring(log.kubernetes.labels),
        pod_name = tostring(log.kubernetes.pod_name),
        region = tostring(log.region),
        tag,
        container_status_image_id = tostring(log.kubernetes.container_hash),
        cluster_id = tostring(log.cluster_id),
        resource_group = tostring(log.resource_group)
}

.alter table hcpContainerLogs policy update
@'[{"IsEnabled": true, "Source": "rawLogs", "Query": "rawToHcpContainerLogs()", "IsTransactional": true, "PropagateIngestionProperties": false}]'
```

A service table (`frontendLogs`) follows the same shape — the `project` body is a
mechanical translation of that table's old ingestion mapping, filtered by
`container_name startswith "aro-hcp-frontend"`. Snapshot tables filter on
`snapshotType`, which collapses the old trio of `rewrite_tag` filters into a single
policy per snapshot table.

### Transactional behaviour & failure modes

Update policies over native tables run **synchronously as part of ingestion**.

- All policies are `IsTransactional: true` (required in production, and required
  whenever the source table uses `softdelete = 0s`). A transform error therefore
  fails the `rawLogs` ingestion too. Because `rawLogs` is the single funnel for all
  logs, a broken function is load-bearing for everything — so the emulator gate
  (`make verify-kql`) and the routing test are mandatory for any KQL change, and
  the raw table uses a short `softdelete = 1h` during bake-in before being tightened
  to `0s`.
- Ingestion cost scales with the number of target tables sharing the source
  (~12 policies over `rawLogs`). Each query is a cheap `where … | project …`, but
  fan-out cost is measured in dev before promotion.
- Inspect update-policy failures with
  `.show ingestion failures | where OriginatesFromUpdatePolicy == true`.

## Data Ingestion

### Fluent Bit Forwarder

Fluent Bit runs as a DaemonSet (`arobit-forwarder`) on each AKS cluster:

- **Inputs**: container logs from `/var/log/containers/*.log`, host `systemd`/journald, `/var/log/azure-vnet.log`, forward protocol (port 24224), Fluent Bit metrics.
- **Filters**: CRI reassembly, metadata enrichment (`environment`, `region`, `cluster`), Kubernetes metadata, container-log JSON parsing, and (on management clusters) the Lua step that stamps `cluster_id`/`resource_group` from the HCP namespace annotation. All **Kusto-routing** `rewrite_tag`/`grep` filters (`container_name_router`, the snapshot routers, `drop_kube_applier_snapshots`) are removed — that routing moves to update policies. The `namespace_router` (`ocm-*` → `ocm.logs`) is **retained** because the Geneva/Shoebox fork depends on it (see [Geneva/Shoebox forwarding](#genevashoebox-forwarding-unchanged)).
- **Output**: a single Azure Kusto output writing the enriched record to `AllAroHcpLogs.rawLogs` via the `rawMapping` ingestion mapping. It matches both `kubernetes.*` and `ocm.logs` (the `namespace_router` re-tags `ocm-*` records, and the Shoebox fork keeps a copy) and must **not** match `shoeboxlogs`. On mgmt+mdsd clusters the Geneva/Shoebox forward output runs in parallel (below).

### Authentication

Fluent Bit authenticates using **Azure Workload Identity** (MSI client ID, token from `AZURE_FEDERATED_TOKEN_FILE`). Explicit ingestion endpoints are configured per cluster via Helm values. The forwarder's cluster-log MSI is granted `Ingestor` on `AllAroHcpLogs`; readers (admin API, exporter, Grafana) are granted `Viewer`.

### Geneva/Shoebox forwarding (unchanged)

On **management clusters with `mdsd.enabled`**, customer control-plane logs are also
forwarded to Geneva as Azure Shoebox diagnostic logs — a **separate sink from
Kusto**. This path is **not** replaced by update policies (Geneva is not an ADX
database; update policies cannot forward to it) and is preserved as-is:

1. `filter.namespace_router` re-tags `ocm-*` namespace records to `ocm.logs`.
2. `lua.set_cluster_id_from_annotation` (mgmt) stamps `cluster_id`/`resource_group`
   on `ocm.logs` — the Shoebox transform reuses this `cluster_id`, so it must run
   first.
3. `filter.shoebox_router` (mgmt+mdsd) forks control-plane containers
   (`audit-logs`, `kube-apiserver`, `kube-controller-manager`, `kube-scheduler`,
   `cloud-controller-manager`, `csi-driver`, `snapshot-controller`,
   `cluster-autoscaler`, `capi-provider.*`) to `shoeboxlogs` with **KEEP=true**, so
   the Kusto copy survives.
4. `lua.transform_shoebox` maps to the Azure Shoebox schema and `forward.shoebox`
   ships to the MDSD clusterlogs sidecar on `127.0.0.1:5002`.

> **Do not remove `filter.namespace_router` when simplifying the Kusto routing.**
> The Shoebox fork matches the `ocm.logs` tag it produces; deleting it silently
> stops customer logs from reaching Geneva. Because this path only runs on
> mgmt+mdsd (dev clusters may not have mdsd enabled), the behavioural e2e may not
> exercise it — guard it with a Helm-fixture assertion on a mgmt+mdsd render that
> `filter-shoebox`/`output-shoebox` still render and the `forward` to port `5002`
> exists.

## Querying Logs

### Azure VNet CNI Logs

When Kusto forwarding is enabled, Arobit tails `/var/log/azure-vnet.log` on service and management nodes using Fluent Bit's native tail input and JSON parser. No additional agent is required. The parser uses the JSON `ts` UTC timestamp (retained in `log.ts`). Plain-text and malformed JSON lines are retained as `message` with collection time and no parsed severity. Escaped newlines within JSON do not need multiline reassembly.

The dynamic `log` column retains parsed diagnostic fields, including `error`, caller, and available network identifiers. Top-level `stdinData` and `args` are removed before export because they contain CNI invocation/configuration payloads. Unparsed lines containing either field name are dropped rather than forwarding a potentially truncated payload. This is not general-purpose redaction: messages and other fields can still contain infrastructure and customer network identifiers. Treat this table as service diagnostic data, not customer-visible logs. Errors can be logged at `info`, so do not filter solely by severity.

```kusto
azureVnetLogs
| where timestamp between (datetime(2026-09-17T11:50:00Z) .. datetime(2026-09-17T12:20:00Z))
| where cluster == '<management-cluster>' and hostname == '<node>'
| where message contains 'DHCP' or tostring(log.error) contains 'dhcp'
| project timestamp, hostname, level, message, log
| order by timestamp asc
```

Always include `cluster` as well as `hostname` when correlating with `systemdLogs` or Pod snapshots. Azure VNet files are not included in existing hcpctl systemd exports; query this table directly.

Only the active file is watched; numeric and timestamp-named backups are not backfilled. The tail cursor is stored in `/var/log/flb-azure-vnet.db` on the existing host mount, so it survives collector Pod replacement on the same node. A new cursor starts at the end of the file. Rotation is followed with a 30-second wait for the old file; lines over 256 KiB are skipped rather than stopping the reader. Node replacement, rotations during collector downtime, and ephemeral output buffers can still lose data. Collection begins only after this configuration is deployed; use [manual collection](ops/mitigate-swiftv2-wedged-node.md) for retained files from earlier incidents.

Deploy the `azureVnetLogs` table and ingestion mapping through the Geography Kusto infrastructure pipeline before rolling out the collector. A Region-only rollout does not deploy that ancestor. No new database ingestion identity is needed.

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

**Best Practices**: Always specify time ranges, filter on `resource_id`/`subscription_id`/`cluster_id`, use `limit`, filter on indexed fields (`timestamp`, `cluster`, `namespace_name`), and use `project` to select needed columns. Query HCP container logs from `hcpContainerLogs` (not `containerLogs`).

Follow up read: https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/runbooks/kustoqueries

## Infrastructure

Kusto infrastructure is defined in `dev-infrastructure/modules/logs/kusto/` and deployed via the Geography Kusto pipeline (`dev-infrastructure/geography-pipeline.yaml`):

- **main.bicep**: orchestrates the cluster, databases, table scripts, and permissions. Loads the `tables/allarohcplogs/*.kql` set in deterministic order (the raw table and routing helpers are numeric-prefixed so they are created before the policies that reference them).
- **cluster.bicep**: cluster resource definition and cluster-level role assignments.
- **database.bicep**: database creation with retention policies.
- **script.bicep**: KQL script execution for table/function/policy creation.
- **grant-access.bicep** / **kusto-grant-ingest.bicep**: ingestion and viewer role assignments.

Configuration is in `config/config.yaml` (`kusto.allAroHcpLogsDatabase`, and the `arobit.kusto.*` forwarder wiring).

## Monitoring and Verification

- **KQL validation**: `make verify-kql` applies every `.kql` file to a local Kusto emulator (`hack/kql-verify.sh` → `tooling/kustoctl validate kql`). This validates schema and update-policy creation (the function output schema must match the target table). A separate validation database is used for the `allarohcplogs/` set so its table names do not collide with the legacy tables.
- **Routing test**: a hermetic emulator test ingests sample rows into `rawLogs` and asserts each lands in exactly one target table with the correct field values, plus that `hcpContainerLogs` retention is 14 days. See the testing strategy in [`logging-allarohcplogs-spec.md`](logging-allarohcplogs-spec.md).
- **End-to-end**: `test/e2e/kusto_logs_present.go` and `test/util/verifiers/kusto.go` create an HCP cluster and assert logs are queryable for the `aro-hcp` (`aro-hcp-frontend`, `aro-hcp-backend`) and `clusters-service` services and for `ocm-*` (`kube-apiserver`, via `hcpContainerLogs`).
- **Freshness canary**: the `aro-hcp-exporter` emits `kusto_logs_age_in_seconds{table=…}`, the primary signal for ingestion stalls and for each migration cutover.
- Automated cleanup of stale role assignments is available in `test/util/cleanup/kusto-role-assignments/`.

## Deprecated: legacy dual-database setup

> **⚠️ Deprecated and scheduled for deletion.** The setup in this section is being
> replaced by [`AllAroHcpLogs`](#single-database-design-allarohcplogs). Do **not**
> add new tables, ingestion mappings, forwarder outputs, or readers to it.

The previous design used **two** databases and performed all routing in the Fluent
Bit forwarder:

- **ServiceLogs** (90 days soft delete, 2 days hot cache): `frontendLogs`, `backendLogs`, `kubeApplierLogs`, `clustersServiceLogs`, `fleetLogs`, `containerLogs` (non-OCM), `kubernetesEvents`, `systemdLogs`, `azureVnetLogs`, `kubernetesResourceSnapshots`, `cosmosResourceSnapshots`.
- **HostedControlPlaneLogs** (14 days soft delete, 2 days hot cache): `containerLogs` and `kubernetesEvents` from OCM namespaces (`ocm-*`). *(Originally named `customerLogs`.)*

The forwarder used ~13 `azure_kusto` outputs fed by `rewrite_tag` filters that
routed by namespace (`ocm-*` → `HostedControlPlaneLogs`), by `container_name` (per
service), and by `snapshotType`. Each table carried its column extraction in a
per-table ingestion mapping reading `$.log.*`.

Legacy routing (for reference during migration):

| Source | Namespace | Container Pattern | Database | Table |
|--------|-----------|-------------------|----------|-------|
| Frontend | Non-OCM | `aro-hcp-frontend*` | ServiceLogs | `frontendLogs` |
| Backend | Non-OCM | `aro-hcp-backend*` | ServiceLogs | `backendLogs` |
| Clusters Service | Non-OCM | `clusters-service*` | ServiceLogs | `clustersServiceLogs` |
| Fleet | Non-OCM | `fleet-controller*` | ServiceLogs | `fleetLogs` |
| kube-applier | Non-OCM | `kube-applier*` | ServiceLogs | `kubeApplierLogs` |
| Kubernetes Events | Any | `kube-events*` | ServiceLogs | `kubernetesEvents` |
| Azure VNet host file | N/A | N/A | ServiceLogs | `azureVnetLogs` |
| General Containers | Non-OCM | Other | ServiceLogs | `containerLogs` |
| HCP Containers | `ocm-*` | Any | HostedControlPlaneLogs | `containerLogs` |
| HCP Events | `ocm-*` | `kube-events*` | HostedControlPlaneLogs | `kubernetesEvents` |

**Deletion plan.** After readers (hcpctl, the exporter, e2e verifiers, cleanup
tooling) are repointed at `AllAroHcpLogs` and the forwarder's legacy outputs are
removed, the `ServiceLogs` and `HostedControlPlaneLogs` databases are removed via
`dev-infrastructure/cleanup/delete.kusto.instance.pipeline.yaml`. The full phased
migration and rollback are in [`logging-allarohcplogs-spec.md`](logging-allarohcplogs-spec.md).

## References

- [Implementation spec: `AllAroHcpLogs`](logging-allarohcplogs-spec.md)
- [Must-Gather Commands Documentation](sops/gather-logs.md)
- [Monitoring Documentation](monitoring.md)
- [Azure Data Explorer Documentation](https://learn.microsoft.com/en-us/azure/data-explorer/)
- [Kusto update policy overview](https://learn.microsoft.com/en-us/kusto/management/update-policy?view=microsoft-fabric)
- [Fluent Bit Azure Kusto Output Plugin](https://docs.fluentbit.io/manual/pipeline/outputs/azure-kusto)
