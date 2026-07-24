---
name: trace-request
description: Trace all data related to an ARM request through the system.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

## What I Do

Given an ARM correlation ID and a time window, run a series of chained KQL
queries against a Kusto cluster to gather every piece of data related to a
single ARM request as it flows through the system: frontend logs, backend state
transitions, Cluster Service phases, Maestro bundle delivery, and Kubernetes
events for each component.

The first query uses the correlation ID to discover the ARM resource ID from
frontend logs. All downstream queries are derived from the parsed resource ID
(resource group, resource name, resource type). Queries that depend on earlier
results are automatically skipped when prerequisites are not satisfied (e.g.
Cluster Service queries are skipped for resource types that don't apply).

## When to Use Me

Use this skill when the user wants to:
- trace a single ARM request end-to-end across all service components
- gather all logs related to a specific correlation ID
- understand the lifecycle of an ARM operation (create, update, delete)
- debug why a specific ARM request failed at any layer of the stack

## Parameters

Only ask for a parameter if it is required and was not provided by the user.

| Parameter | Make Variable | Required | Description |
|-----------|--------------|----------|-------------|
| Config file | `CONFIG_FILE` | yes | Path to a `config.yaml` containing Kusto connection details. The Makefile extracts cluster name, region, and database names from this file automatically. |
| Start time | `START_TIME` | yes | Start of the time window to query, in RFC3339 format (e.g. `2026-04-06T02:03:13Z`). |
| End time | `END_TIME` | yes | End of the time window to query, in RFC3339 format (e.g. `2026-04-06T02:33:13Z`). |
| Correlation ID | `CORRELATION_ID` | yes | ARM correlation ID for the request to trace. |
| Output directory | `OUTPUT_DIR` | yes | Directory to write query results into. |

## How to Derive Each Parameter

### Config file

The `config.yaml` is produced by the `gather-job-data` skill and lives in the
job run directory. Set `CONFIG_FILE` to its path.

The Makefile uses `yq` to extract the following fields at runtime:
- `.region` -- passed as `--region`
- `.kusto.name` -- passed as `--cluster-name`
- `.kusto.hostedControlPlaneLogsDatabase` -- passed as `--hcp-database`
- `.kusto.serviceLogsDatabase` -- passed as `--service-database`

### Start time and end time

Provide the time window to query as RFC3339 timestamps. These may come from test
metadata, user input, or any other source. A good starting point for test
failures is the test start and end times from `metadata.json`:

```shell
START_TIME=$(date -u -d "$(jq -r .startTime "$TEST_METADATA_FILE")" +%Y-%m-%dT%H:%M:%SZ)
END_TIME=$(date -u -d "$(jq -r .endTime "$TEST_METADATA_FILE")" +%Y-%m-%dT%H:%M:%SZ)
```

### Correlation ID

The ARM correlation ID is typically found in frontend logs or test output. Use
the `frontend_mutating_requests_correlation_id` query from the `triage-test`
skill to discover correlation IDs for mutating requests in a given time window
and resource group.

### Output directory

Choose a directory to write results into. Each query writes three files:
- `<output-dir>/<component>/<query-name>/query.kql` -- the rendered KQL query
- `<output-dir>/<component>/<query-name>/output.json` -- the raw Kusto v2 JSON response
- `<output-dir>/<component>/<query-name>/output.md` -- the results as a Markdown table

## How to Run

Once all parameters are determined, run `make run` in this skill's directory:

```bash
make run \
  CONFIG_FILE=<path-to-config.yaml> \
  START_TIME=<rfc3339-start> \
  END_TIME=<rfc3339-end> \
  CORRELATION_ID=<correlation-id> \
  OUTPUT_DIR=<output-dir>
```

The working directory must be `tooling/triage/skills/trace-request`.

## Output

Results are organized under the output directory by component and query name:

```
<output-dir>/
  frontend/
    resourceId/        -- ARM resource ID from correlation ID
    asyncOperationId/  -- async operation ID (location stripped)
    asyncOperationPath/ -- async operation path
    asyncOperationRequests/ -- async operation polling requests
    events/            -- Kubernetes events for frontend pods
  backend/
    asyncOperationState/      -- async operation state transitions
    resourceState/            -- resource state over time
    resourceControllerConditions/ -- controller conditions
    serviceProviderState/     -- service provider sub-resource state
    resourceInternalId/       -- Cluster Service internal ID
    events/                   -- Kubernetes events for backend pods
  clustersService/
    cid/               -- Cluster Service cluster ID
    phases/            -- cluster/nodepool phase transitions
    clusterState/      -- Cluster Service cluster state
    maestroBundles/    -- Maestro manifest work bundle IDs
    events/            -- Kubernetes events for clusters-service pods
  maestro/
    events/            -- Kubernetes events for maestro pods
    serverLogs/        -- maestro-server logs for bundles
    agentLogs/         -- maestro-agent logs for bundles
  hypershift/
    pkiOperatorEvents/ -- PKI operator events (requestadmincredential only)
    hostedClusterConditions/ -- HostedCluster conditions (hcpopenshiftclusters only)
```

Queries that are not applicable for the resource type or whose prerequisites
were not satisfied are skipped and produce no output.

Each query directory contains `query.kql` (the rendered query), `output.json`
(the raw Kusto response), and `output.md` (the results as a Markdown table).
Use the `kusto-query-table` skill to re-render any `output.json` file as a
Markdown table if needed.

### Maestro Server Log Messages — Data Flow Reference

```
Source Client → Server → Agent → Server → Source Client
```

| Log Message | Flow Stage | Direction |
|---|---|---|
| `receive the event from client` | Server receives spec from source client via gRPC `Publish()` | **Inbound** |
| `Sending event` | Server sends spec CloudEvent to broker (toward agent) | **Outbound to agent** |
| `Received event` | Server receives status CloudEvent back from agent | **Inbound from agent** |
| `Updating resource status` | Server persists status to database | **Internal** |
| `send the event to status subscribers` | Server broadcasts status to gRPC `Subscribe()` streams | **Outbound to source** |

### Maestro Agent Log Messages — Data Flow Reference

```
Agent receives spec → applies/deletes manifests on target cluster → reports status back
```

| Log Message | Flow Stage | Description |
|---|---|---|
| `Received event` | **Receive — spec** | Agent receives a spec CloudEvent from the broker (OCM SDK `baseClient.subscribe()`) |
| `Patching resource` | **Apply — ownership** | Agent patches owner references on an existing resource (merge-patch to set/update `AppliedManifestWork` ownership) |
| `Server side applied` | **Apply — spec** | Agent applied the manifest to the target cluster via server-side apply (`Resource.Apply()`) |
| `Sending event` | **Report — status** | Agent publishes a status CloudEvent back to the broker (OCM SDK `baseClient.publish()`) |
| `Deleted resource` | **Delete — issued** | Agent sent a delete request for the resource; it may still be pending finalization |
| `Resource is removed successfully` | **Delete — confirmed** | Agent confirmed the resource no longer exists on the target cluster (404 on GET) |