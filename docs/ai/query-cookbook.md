# Agentic Kusto Query Cookbook for ARO HCP

- IMPORTANT: this document is referenced by agentic workflows, DO NOT REMOVE IT.
- If any of the info below turns out not to be accurate, suggest to the user an update PR at the end of the session.

This is an index of curated, production-tuned KQL queries used by the
`hcpctl snapshot` tool to trace an ARM request layer-by-layer through the
ARO HCP stack. Each query lives in
[tooling/hcpctl/pkg/snapshot/queries/](../../tooling/hcpctl/pkg/snapshot/queries/)
next to a `README.md` documenting intent, sample healthy output, and which
query to run next.

When debugging interactively, prefer to read the relevant `query.kql` file
and adapt it (the queries take constants like resource group, time window,
cluster URI as parameters that must be filled in) rather than writing from
scratch — the canned queries already encode the right table/column/filter
choices.

## Architectural request flow

```
Customer → ARM → Frontend → Backend (async) → Clusters Service → Maestro → HyperShift
```

- **Frontend** validates the ARM request and creates an async operation.
- **Backend** picks up the async op and translates it into Clusters Service API calls.
- **Clusters Service (CS)** runs its own state machine and produces Maestro resource bundles.
- **Maestro server** (service cluster) delivers bundles to the **Maestro agent** (management cluster) as ManifestWork.
- **HyperShift** reconciles HostedCluster/NodePool CRs into actual control plane pods.

Hosted cluster namespace convention: `ocm-arohcp<env>-<cid>-<id>`, where
`cid` is the Clusters Service internal cluster ID (an opaque hash like
`2iig1flm0pfjr9h8kkg6ggbjig1p3fpa`).

The two Kusto databases per region (see [kusto-debugging.md](kusto-debugging.md) for the table list):

- `ServiceLogs` — frontend, backend, clusters service, fleet — i.e. the service cluster microservices.
- `HostedControlPlaneLogs` — HyperShift, control-plane-operator, per-cluster control plane container logs, and management-cluster components.

## Bootstrapping: from minimal input to magic strings

Most queries need a `client_request_id`, the CS internal cluster ID (`cid`),
or the cluster's Azure resource coordinates. The discovery queries resolve
those from whatever you have at hand.

| Have | Query | Resolves |
|---|---|---|
| `correlation_request_id` | [frontend/clientRequestId](../../tooling/hcpctl/pkg/snapshot/queries/frontend/clientRequestId/README.md) | All `client_request_id`s in the correlation group |
| `correlation_request_id` | [frontend/asyncOperationId](../../tooling/hcpctl/pkg/snapshot/queries/frontend/asyncOperationId/README.md) | Async operation ARM resource ID |
| `client_request_id` | [frontend/asyncOperationPath](../../tooling/hcpctl/pkg/snapshot/queries/frontend/asyncOperationPath/README.md) | Async operation path |
| ARM resource ID | [backend/resourceInternalId](../../tooling/hcpctl/pkg/snapshot/queries/backend/resourceInternalId/README.md) | CS internal URI for the resource |
| ARM resource ID | [clustersService/cid](../../tooling/hcpctl/pkg/snapshot/queries/clustersService/cid/README.md) | CS internal cluster ID (`cid`) |


If `clustersService/cid` returns nothing, something has gone catastrophically
wrong with CS — go directly to [clustersService/events](../../tooling/hcpctl/pkg/snapshot/queries/clustersService/events/README.md).

## Per-layer query index

### Frontend (ARM-facing)

| Query | Purpose |
|---|---|
| [frontend/frontendRequests](../../tooling/hcpctl/pkg/snapshot/queries/frontend/frontendRequests/README.md) | All mutating ARM requests in scope |
| [frontend/asyncOperationRequests](../../tooling/hcpctl/pkg/snapshot/queries/frontend/asyncOperationRequests/README.md) | Polling history for each async op |
| [frontend/requestLogs](../../tooling/hcpctl/pkg/snapshot/queries/frontend/requestLogs/README.md) | Full request log lines for a `client_request_id` |
| [frontend/events](../../tooling/hcpctl/pkg/snapshot/queries/frontend/events/README.md) | Frontend pod K8s events |

### Backend (async worker)

| Query | Purpose |
|---|---|
| [backend/resourceState](../../tooling/hcpctl/pkg/snapshot/queries/backend/resourceState/README.md) | External (customer-facing) ARM resource document over time, ordered by etag |
| [backend/serviceProviderState](../../tooling/hcpctl/pkg/snapshot/queries/backend/serviceProviderState/README.md) | Internal RP-owned state for the resource |
| [backend/resourceControllerConditions](../../tooling/hcpctl/pkg/snapshot/queries/backend/resourceControllerConditions/README.md) | **Snapshot** of HCP controller conditions (look for `Degraded=true`) |
| [backend/resourceControllerConditionTimeline](../../tooling/hcpctl/pkg/snapshot/queries/backend/resourceControllerConditionTimeline/README.md) | **Timeline** of every controller condition transition |
| [backend/events](../../tooling/hcpctl/pkg/snapshot/queries/backend/events/README.md) | Backend pod K8s events |

Pattern: condition queries come in **snapshot** (last seen state in the
time window) and **timeline** (every transition) pairs. Check snapshot
first, drop to timeline if the snapshot doesn't explain the failure.

### Clusters Service

| Query | Purpose |
|---|---|
| [clustersService/phases](../../tooling/hcpctl/pkg/snapshot/queries/clustersService/phases/README.md) | Cluster/node-pool lifecycle phase transitions (`validating` → `pending` → `installing` → `ready` → `uninstalling`) |
| [clustersService/clusterState](../../tooling/hcpctl/pkg/snapshot/queries/clustersService/clusterState/README.md) | Full cluster object over time (from `csstatedump`) |
| [clustersService/nodePoolState](../../tooling/hcpctl/pkg/snapshot/queries/clustersService/nodePoolState/README.md) | Same for node pools |
| [clustersService/logs](../../tooling/hcpctl/pkg/snapshot/queries/clustersService/logs/README.md) | CS structured logs |
| [clustersService/events](../../tooling/hcpctl/pkg/snapshot/queries/clustersService/events/README.md) | CS pod K8s events |


Rule of thumb: if `phases` shows the cluster never left `validating` or
`pending`, the failure is in CS or upstream; if it gets to `installing` but
never `ready`, the failure is in HyperShift or further downstream.

### Maestro

> **Note:** the Maestro snapshot queries (`maestro/transitions`,
> `maestro/serverLogs`, `maestro/agentLogs`, `maestro/mgmtAuditLogs`,
> `maestro/events`) and both `maestroBundleAssociations` queries were
> **removed from hcpctl** (PR #7810) — the snapshot no longer collects
> Maestro-side data. Maestro remains part of the CS delivery path, but its
> server/agent logs must be gathered directly when needed.

### kubeApplier

| Query | Purpose |
|---|---|
| [kubeApplier/logs](../../tooling/hcpctl/pkg/snapshot/queries/kubeApplier/logs/README.md) | kube-applier (management cluster) logs — reconciles the cluster's ApplyDesire/ReadDesire documents against the management-cluster apiserver; each row carries `controller_name` and `resource_id` |

### HyperShift (management cluster)

| Query | Purpose |
|---|---|
| [hypershift/hostedClusterConditions](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/hostedClusterConditions/README.md) | HC condition snapshot (focus on `Available`) |
| [hypershift/hostedClusterConditionTimeline](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/hostedClusterConditionTimeline/README.md) | HC condition transitions over time |
| [hypershift/nodePoolConditions](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/nodePoolConditions/README.md) | NodePool condition snapshot |
| [hypershift/nodePoolConditionTimeline](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/nodePoolConditionTimeline/README.md) | NodePool condition transitions over time |
| [hypershift/hostedClusterMetadata](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/hostedClusterMetadata/README.md) | Point-in-time HC object metadata |
| [hypershift/hypershiftOperatorLogs](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/hypershiftOperatorLogs/README.md) | HyperShift operator logs, aggregated by message |
| [hypershift/controlPlaneOperatorLogs](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/controlPlaneOperatorLogs/README.md) | Control plane operator logs |
| [hypershift/clusterAPILogs](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/clusterAPILogs/README.md) | CAPI controller logs (Machine lifecycle) |
| [hypershift/clusterAPIProviderLogs](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/clusterAPIProviderLogs/README.md) | CAPZ logs (Azure resource management for VMs) |
| [hypershift/controlPlaneEvents](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/controlPlaneEvents/README.md) | K8s events from the hosted control plane namespace |
| [hypershift/events](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/events/README.md) | HyperShift namespace events |
| [hypershift/pkiOperatorEvents](../../tooling/hcpctl/pkg/snapshot/queries/hypershift/pkiOperatorEvents/README.md) | PKI operator events |

The operator log queries `| summarize` by `(message, controller, resource)`
with first/last seen and count, so the output is a deduplicated pattern list
rather than a raw stream — designed to fit in a context window.

## Debugging methodology

This is distilled from `tooling/hcpctl/pkg/agent/prompts/system/` (see
`60_debugging_methodology.intent.md`), which drives the `hcpctl snapshot
analyze` agent. It generalizes well to any interactive debugging session.

1. **Anchor in the failure.** Start with the test/operation error to identify
   the failing client/server relationship: ARM, the hosted cluster's kube
   API, or a workload on the hosted cluster.
2. **Walk the layers in order.** Frontend → Backend → CS → Maestro →
   HyperShift. Don't skip — a CAPI error often has a CS root cause one
   layer up.
3. **Status before logs.** Check controller/resource conditions
   (`backend/resourceControllerConditions`, `hypershift/hostedClusterConditions`,
   `hypershift/nodePoolConditions`) before grepping logs. Timelines next, then
   raw logs, then pod events.
4. **Check kube-applier logs.** [kubeApplier/logs](../../tooling/hcpctl/pkg/snapshot/queries/kubeApplier/logs/README.md)
   shows the management-cluster desire-application side (controller name and
   resource per row); Maestro-side data is no longer collected in the snapshot.
5. **Timeouts are a symptom, not a cause.** Keep drilling.
6. **Demonstrate absence.** Use `| summarize count = count()` to prove
   something didn't happen — empty result sets aren't proof on their own.
7. **Compare against passing siblings.** If the same job ran similar tests
   that passed, their log traces are the ground truth for what "good"
   looks like.

## Worked-example analyses

End-to-end analyses (causal chain, queries, code citations)
live in
[tooling/hcpctl/pkg/agent/prompts/exemplars/](../../tooling/hcpctl/pkg/agent/prompts/exemplars/):

- `cluster-creation-timeout-z-stream-upgrade.md`
- `cluster-deletion-timeout-because-managedcluster-cr-deletion-stuck.md` — cluster deletion stuck at ManagedCluster destructor due to addon pod eviction (MemoryPressure)
- `cluster-installation-azure-disk-failure.md`
- `cluster-installation-ignition-failure.md`
- `clusters-service-worker-race-condition.md`

Read these when you need a concrete template for what a thorough RCA looks
like in this stack.

## See also

- [kusto-debugging.md](kusto-debugging.md) — table reference and common patterns.
- [grafana-debugging.md](grafana-debugging.md) — metrics-side debugging.
- [tooling/hcpctl/pkg/agent/prompts/references/architecture.md](../../tooling/hcpctl/pkg/agent/prompts/references/architecture.md) — fuller architecture reference.
- [tooling/hcpctl/pkg/agent/prompts/references/service-components.md](../../tooling/hcpctl/pkg/agent/prompts/references/service-components.md) — per-component responsibilities and failure modes.
- [docs/snapshot-analysis.md](../snapshot-analysis.md) — the `hcpctl snapshot` tooling overview.
