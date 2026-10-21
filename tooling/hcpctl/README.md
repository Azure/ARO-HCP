# hcpctl - ARO HCP SRE CLI Tool

A CLI tool for ARO HCP operations, including emergency access (breakglass) functionality into service clusters, management clusters and hosted clusters.

## Purpose

`hcpctl` provides SREs with emergency access capabilities for ARO HCP infrastructure:

- **Service Cluster (SC) access**: Get shell access to AKS service clusters
- **Management Cluster (MC) access**: Get shell access to AKS management clusters
- **Hosted Control Plane (HCP) access**: Break glass into customer HCP clusters for emergency operations
- **Azure AD authentication**: Integrated kubelogin for seamless Entra authentication with AKS clusters

## Installation

The tool will be available on SAW devices similar to how the `oc` utility is accessed. `hcpctl` works in the Red Hat DEV environment, Microsoft INT environment, and on SAW devices to access stage and production in AME.

## Prerequisites

- Azure CLI authentication
- JIT permissions for target clusters (currently Azure Kubernetes Service RBAC Cluster Admin role, subject to change)

## Commands Overview

### Service Cluster Operations (`sc`)

- `hcpctl sc list` - List available service clusters
- `hcpctl sc breakglass <cluster-name>` - Get access to service cluster

### Management Cluster Operations (`mc`)

- `hcpctl mc list` - List available management clusters
- `hcpctl mc breakglass <cluster-name>` - Get access to management cluster
- `hcpctl mc dump-crs <cluster-name> --hosted-cluster-namespace=<ns>` - Dump Custom Resources for a HostedCluster

### Hosted Control Plane Operations (`hcp`)

- `hcpctl hcp list` - List available HCP clusters
- `hcpctl hcp breakglass <cluster-service-id|azure-resource-id>` - Emergency access to HCP cluster

## Example Usage

### Snapshot Analysis with Claude Code

`hcpctl snapshot analyze` supports `--provider claude-code` in addition to the
existing `copilot` (default) and in-process `claude` providers. Install the Claude
Code CLI on `PATH`. With the default `--claude-backend api`, authenticate the CLI,
set `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN`, or supply
`--anthropic-api-key-file`.

```bash
hcpctl snapshot analyze /path/to/snapshot --provider claude-code \
  --aro-hcp /path/to/ARO-HCP --hypershift /path/to/hypershift \
  --maestro /path/to/maestro --clusters-service /path/to/clusters-service
```

Both Claude providers support `--claude-backend vertex` with Google Application
Default Credentials (ADC). Set `--vertex-project` and `--vertex-region`, or their
environment fallbacks `GOOGLE_VERTEX_PROJECT_ID` and `GOOGLE_VERTEX_REGION`.
The Claude Code provider translates these into the CLI's documented environment
variables: `CLAUDE_CODE_USE_VERTEX=1`, `ANTHROPIC_VERTEX_PROJECT_ID`, and
`CLOUD_ML_REGION`. It does not read the Anthropic API key file for Vertex or pass
Google key material. See the [Claude Code Vertex setup guide](https://code.claude.com/docs/en/google-vertex-ai)
for ADC setup and model availability.

Backend selection overrides inherited CLI backend switches: `api` forces
`CLAUDE_CODE_USE_VERTEX=0` and `CLAUDE_CODE_USE_BEDROCK=0`, while `vertex` forces
Vertex on and Bedrock off. The SDK overlays these values onto the parent
environment, preserving the ambient API key when no key file is supplied.
Claude Code documents `0` as the off value for these switches in its
[environment variable reference](https://code.claude.com/docs/en/env-vars#variables).

Use `--model` to override `claude-sonnet-4-5-20250929`; the CLI SDK requires an
explicit model. `--anthropic-model` applies only to the in-process `claude`
provider. The existing `kusto_query` handler is available to
Claude Code as the in-process MCP tool `mcp__hcpctl__kusto_query`.

System prompts larger than 100 KiB keep the small identity/tone instructions in
the CLI system prompt and send the complete domain instructions and reference
material at the start of the first stdin query. If identity/tone themselves are
too large, all instructions move to that first query under a small system
directive. Nothing is truncated, and subsequent queries do not resend this
content. This avoids Linux's per-argument `execve` size limit (`E2BIG`).

This provider runs unattended with permission checks bypassed in the analysis
workspace. It disables MCP discovery and SDK skills, passes empty settings
sources, and requests exclusion of CLAUDE.md memory. This is **best-effort
configuration isolation, not a sandbox**: managed instructions and explicit file
reads (including CLAUDE.md or AGENTS.md) may still be possible. Run it only with
trusted inputs and the filesystem permissions you intend it to have.

The CLI's session persistence is disabled; hcpctl saves `conversation.json` and
`usage.json` in the analysis output directory. Usage is based on cumulative result
snapshots, with per-model tokens/cost where available; API request counts, cache
TTLs, and service tiers are not available at the in-process provider's granularity.

### List Service Clusters

```bash
# List all available service clusters
hcpctl sc list

# Filter by region
hcpctl sc list --region eastus

# Output as JSON
hcpctl sc list --output json
```

### Breakglass into Service Cluster

```bash
# Get shell access to service cluster
hcpctl sc breakglass int-usw3-svc-1

# Generate kubeconfig only (no shell)
hcpctl sc breakglass int-usw3-svc-1 --output /tmp/sc.kubeconfig --no-shell
KUBECONFIG=/tmp/sc.kubeconfig kubectl get ns
```

### List Management Clusters

```bash
# List all available management clusters
hcpctl mc list

# Filter by region
hcpctl mc list --region eastus

# Output as JSON
hcpctl mc list --output json
```

### Breakglass into Management Cluster

```bash
# Get shell access to management cluster
hcpctl mc breakglass int-usw3-mgmt-1

# Generate kubeconfig only (no shell)
hcpctl mc breakglass int-usw3-mgmt-1 --output /tmp/mc.kubeconfig --no-shell
KUBECONFIG=/tmp/mc.kubeconfig kubectl get ns
```

### Dump Custom Resources for a HostedCluster

```bash
# Dump all CRs for a HostedCluster namespace to current directory
hcpctl mc dump-crs int-usw3-mgmt-1 --hosted-cluster-namespace aro-12345678-abcd-1234-5678-123456789abc

# Dump CRs to a specific directory
hcpctl mc dump-crs int-usw3-mgmt-1 --hosted-cluster-namespace aro-12345678-abcd-1234-5678-123456789abc -o /tmp/hcp-debug
```

### List Hosted Control Planes

```bash
# List HCPs on current management cluster
hcpctl hcp list
```

### Breakglass into HCP

```bash
# Emergency access using cluster ID
hcpctl hcp breakglass 12345678-1234-1234-1234-123456789abc

# Access using Azure resource ID
hcpctl hcp breakglass /subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpClusters/cluster-name

# Privileged access (uses system:masters group instead of system:cluster-readers)
hcpctl hcp breakglass 12345678-1234-1234-1234-123456789abc --privileged
```

## Gather logs from Kusto

### Gather Managed cluster logs

This is the usual use case for must-gather kusto. You can gather logs for a managed cluster from Kusto. You need to be logged into Azure to access Kusto. You need to set kusto and region to point to the Kusto instance containing the desired logs.

What is gathered?

- All service logs, that contain the subscription id and resourcegroup name or are in the cluster namespace (aka hcp logs)
- All Kubernetes events from the mgmt and service cluster (excluding HCP)
- All Kubernetes events from the mgmt cluster withing the HCP namespace
- Cosmos DB document snapshots (`cosmosResourceSnapshots`) for resources in the resource group, written to the custom logs directory
- Frontend operation-status and operation-result requests, including requests without a resource group in their URL. Operation IDs are discovered from Cosmos snapshot metadata for the subscription and resource group, narrowed to the selected clusters when `--cluster-id` or `--cluster-name` is specified.
- Version rollout controller logs (`versionRolloutLogs`) and fleet rollout document snapshots (`versionRolloutSnapshots`) from the discovered infrastructure clusters. These include channel-wide decisions and other clusters' assignments because canary readiness and failure budgets can delay the target cluster's upgrade.
- Optionally: Systemd logs from the management and service cluster (turn on using --collect-systemd-logs)

```bash
hcpctl must-gather  query --kusto $kusto --region $region  --subscription-id $subscription_id --resource-group $resource_group
```

The base backend, frontend, kube-applier, and fleet log queries always use the ARM subscription and resource group scope. They do not filter on Clusters Service IDs, including when `--cluster-id` or `--cluster-name` is specified. The `--cluster-id` flag takes a Clusters Service ID, not an ARM resource ID.

The `detailedInfraOrchestrationLogs` custom query includes Maestro, Hypershift,
and ACM agent logs, identified by a `source` field. Must-gather executes this
query in five-minute output windows to reduce the risk of exceeding Kusto's
result-size limit, while discovering cluster and Maestro bundle IDs over the
entire requested interval. The windows feed the same JSONL file and do not
truncate the requested interval. An unusually large individual window can still
exceed the service limit; such failures remain errors, not successful partial
collections. Other detailed service queries retain their full-range aggregations.

For query diagnostics, library callers can render the same orchestration template
with `TemplateData.Summarize = true`:

```go
data := kusto.NewTemplateDataFromOptions(options)
data.Summarize = true
definition, err := factory.GetCustomQueryDefinition("detailedServiceLogs")
// Handle err, then build the children and select detailedInfraOrchestrationLogs.
queries, err := factory.Build(*definition, data)
```

Only the orchestration child's final output changes: it returns `rows`,
`estimatedBytes`, `firstSeen`, and `lastSeen` grouped by `source`, using exactly
the same discovery and filters as log collection. An empty arm has no summary
row, so consumers must compare against all three expected source names
(`maestro`, `hypershift`, `acm-agent`). Byte estimates are not exact wire sizes.
This is a library rendering option, not a CLI flag. The E2E presence verifier
sets `GathererOptions.SummarizeOrchestrationLogs` to execute one full-range
summary instead of downloading the orchestration payloads. It requires a positive
`rows` count for every arm; an absent or zero-count arm fails validation. Other
source checks are unchanged. Normal must-gather and the separate CLI smoke test
still collect full logs in windows: a successful summary alone does not prove
that the full logs can be collected.

To split output files by pod name (useful for investigating timing and concurrency issues), add `--split-by-pod`:

```bash
hcpctl must-gather  query --kusto $kusto --region $region  --subscription-id $subscription_id --resource-group $resource_group --split-by-pod
```

Without this flag, logs from all pods of the same container are written to a single file (e.g. `cluster_namespace_container.jsonl`). With `--split-by-pod`, each pod gets its own file (e.g. `cluster_namespace_container_pod.jsonl`).

If you get an error like, limit execeeded try reducing the amount of data by setting either limit or timestamps, i.e.:

Set `--limit` fetch the first `$limit` number of rows.

```bash
hcpctl must-gather  query \
    --kusto aroint --region eastus \
    --subscription-id $subscription_id --resource-group $resource_group
    --limit 10000
```

The parameters $resource_group and $subscription_id must point to the managed cluster, not the AKS cluster running this HCP/Service.

### Gather infra cluster logs

If you want to gather all Kusto logs for a given infra cluster (servicecluster or management), you can run 

```bash
hcpctl must-gather  query-infra \
    --kusto aroint --region eastus \
    --infra-cluster $svc_cluster_name \
    --infra-cluster $mgmt_cluster_name \
    --limit 10000
```

You can provide multiple `infra-cluster` parameters. Logs will be collected sequentially and stored in a single folder for all clusters provided.

### Limit flag

The --limit flag controls how many log records are returned from each query executed during a must-gather collection. It accepts an integer value and has three distinct behaviors.

#### Default behavior (no --limit specified, or --limit -1)

When you do not specify --limit, it defaults to -1, which means no limit. In this mode, the tool explicitly requests that Azure Data Explorer return all matching results without truncation. This can result in large result sets and longer query times. It can also result in errors on the Kusto side.

#### Positive value

When you specify a positive number, each individual query is capped at that many rows. This is useful when you want a quick sample of logs or when you know the full result set would be impractically large.

The limit applies per query, not to the total output. The must-gather tool internally runs several queries in parallel — for example, one query per log source table (container logs, cluster service logs, frontend logs, backend logs), etc... Each of these queries independently returns up to the specified number of rows. *Important*, results are sorted by timestamp ASC.

#### Interaction with timestamp limitation

`--timestamp-min` and  `--timestamp-max` can be used to limit the result set by filtering the timestamp. The time range filter is always applied first. The limit then caps how many rows are returned from within that time window. You can use this together with limit to iterate over large timeframes.

## Adding Custom Kusto Queries

Custom queries are KQL queries rendered via Go templates. They are embedded into the `hcpctl` binary at build time and can be used both for must-gather data collection and as Kusto deep-links for manual investigation.

### Steps to add a new custom query

1. **Create the template file** in `pkg/kusto/templates/custom/` with a `.kql.gotmpl` extension (e.g. `my_new_query.kql.gotmpl`). Write standard KQL using Go template placeholders for dynamic values (see [Template Data](#template-data) below).

2. **Register the query** in `pkg/kusto/templates/custom/queries.yaml`. For queries composed of multiple related sub-queries, use `children`Children are rendered individually for must-gather collection but merged into a single query for Kusto deep-links.

3. **Build and test.** Templates are embedded via `//go:embed`, so a rebuild is required to pick up changes. Existing query tests in `pkg/kusto/query_test.go` can serve as a reference.

### Template Data

Templates receive a `TemplateData` struct with all values needed to render a KQL query. 

The timestamp fields define the time window for log queries. They are rendered as KQL `datetime(...)` literals (e.g. `datetime(2025-06-15T08:00:00.0000000Z)`)


## TODO

- use the Hypershift generated clientsets instead of dedicated schema registration
- tests for the `pkg/breakglass/minting` package
- tests for the `pkg/breakglass/portforward` package (e.g. https://github.com/openshift/ci-tools/blob/05305124f711827983c0908af9020a41ad6afacf/pkg/testhelper/accessory.go#L261)
- E2E tests for all breakglass commands
