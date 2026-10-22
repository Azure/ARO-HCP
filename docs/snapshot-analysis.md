# Snapshot Gathering and Analysis

The `hcpctl snapshot` commands gather structured diagnostic data for failed
E2E tests and run LLM-driven root cause analysis on the results.

The workflow has two steps:

1. **Gather** — fetch Prow job artifacts, test logs, and Kusto query results
   into a local directory.
2. **Analyze** — send the gathered data to a Copilot agent that produces a
   structured causal chain explaining why the test failed.

`hcpctl snapshot analyze` can also skip the gather step and investigate a
free-form objective directly against Kusto; see
[Dynamic investigations](#dynamic-investigations-no-snapshot).

## Prerequisites

- Azure CLI logged in (`az login`) — needed for Kusto queries.
- GitHub Copilot CLI [installed](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli)
  and authenticated (`gh auth login`) — needed for the analysis agent.
- Local git checkouts of the four repositories at the commits that were deployed
  when the test ran: ARO-HCP (this repository),
  [HyperShift](https://github.com/openshift/hypershift),
  [Maestro](https://github.com/openshift-online/maestro),
  and [Clusters Service](https://github.com/openshift-online/aro-hcp-clusters-service).

## Step 1: Gather

```bash
hcpctl snapshot from-prow-job \
  --url https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-Azure-ARO-HCP-main-aro-hcp-e2e-parallel/1234567890 \
  --test TestNodePoolCreation
```

The `--test` flag is a substring match against test names in the Prow job. The
command fetches the job's JUnit results, identifies the matching failed test,
and gathers:

- `manifest.json` — test metadata, Kusto cluster/database info, time windows.
- `test_logs/error.log` and `test_logs/output.log` — the test's stderr and stdout.
- `sibling_tests.json` — metadata about other tests in the same job (useful for
  identifying systemic failures).
- Per-resource Kusto query results organized by phase (`test_phase/`, `cleanup_phase/`).

The output directory defaults to `snapshot-<timestamp>/` and can be overridden
with `--output-dir`.

## Step 2: Analyze

```bash
hcpctl snapshot analyze ./snapshot-20250101-120000/periodic-ci-.../1234567890/TestNodePoolCreation \
  --aro-hcp ~/code/ARO-HCP \
  --hypershift ~/code/hypershift \
  --maestro ~/code/maestro \
  --clusters-service ~/code/clusters-service
```

The argument is the data directory produced by `gather` (the directory
containing `manifest.json`).

The agent runs through several phases:

1. **Initial prompt** — the agent receives the manifest, test logs, and data
   directory contents, and produces a draft causal chain as structured JSON.
2. **Validation loop** — the draft is parsed and validated (correct JSON
   structure, valid repository references, syntactically correct Kusto queries,
   non-empty code excerpts). If validation fails, the agent receives feedback
   and re-emits corrected output. This repeats for up to `--max-rounds`
   iterations.
3. **Hydration** — Kusto queries from the draft are executed against the real
   cluster, and code excerpt references are resolved from the local git
   checkouts. The results are embedded into the chain.
4. **Review rounds** — the agent sees its own output rendered as a complete
   markdown document and reviews it for coherence, evidence quality, depth,
   and accuracy. It re-emits corrected JSON, which goes through validation
   and hydration again. This repeats for `--review-rounds` iterations.

### Analyze output

After a successful analysis, the command writes four files to the output
directory (defaults to the data directory, overridable with `--output`):

- `analysis.json` — the fully hydrated causal chain as structured JSON.
- `analysis.md` — the chain rendered as a readable markdown document.
- `conversation.json` — the full conversation history with the agent,
  including all prompts, responses, and tool calls.
- `usage.json` — aggregate LLM usage with a breakdown by provider, model, and
  pricing dimensions. Token counts distinguish uncached input, cache reads,
  cache writes, and output. Cache writes include a TTL breakdown when the
  provider reports one (for example, Anthropic's `5m` and `1h` tiers).
  Copilot usage is captured from the live SDK event stream and includes
  automatic conversation-compaction requests.

Once an LLM session has been created, `usage.json` is also written on a
best-effort basis when analysis fails or is cancelled. This preserves usage
from completed requests that may already be billable. `conversation.json` is
likewise saved best-effort on unsuccessful runs; `analysis.json` and
`analysis.md` are success-only outputs.

The report also preserves provider-reported billing units and non-token usage,
such as server-side tool requests. The token categories are normalized to be
mutually exclusive, so a cost calculator can apply the appropriate rate to
each category without double-counting cached input. Pricing is intentionally
not embedded in hcpctl because it varies by provider, model, backend, service
tier, region, contract, and date. The `breakdown` entries preserve the pricing
dimensions reported by the selected provider and backend. The top-level
`startedAt` and `endedAt` fields identify the usage interval so a consumer can
select rates effective at that time. Contract-specific rates and any pricing
context that the provider does not report must be supplied by the external
price book.

The aggregate token categories, additional units, and provider-reported costs
are also logged in the `Analysis complete` message at the default verbosity
level.

## Dynamic investigations (no snapshot)

`--dynamic <objective>` (or `--dynamic-file <path>`) runs an investigation
driven entirely by a free-form objective, without gathering anything first.
There is no data directory: the command takes no positional argument and never
reads a snapshot, manifest, test logs, or other gathered artifacts. Instead, the
agent runs the snapshot query library's queries itself through query library
tools, and writes ad-hoc KQL with the `kusto_query` tool when the library does
not cover a question.

```bash
hcpctl snapshot analyze \
  --dynamic "Why is nodepool np-1 of cluster my-cluster in subscription <sub-id> stuck scaling since 2025-01-01T10:00Z?" \
  --kusto my-adx-cluster --region eastus \
  --aro-hcp ~/code/ARO-HCP \
  --hypershift ~/code/hypershift \
  --maestro ~/code/maestro \
  --clusters-service ~/code/clusters-service \
  --output ./results
```

- `--kusto` and `--region` select the Azure Data Explorer cluster to query.
  `--service-database`, `--hcp-database`, and `--monitoring-events-database`
  override the database names (defaults: `ServiceLogs`,
  `HostedControlPlaneLogs`, `MonitoringEvents`). These flags are only valid in
  dynamic mode; snapshot analysis takes its Kusto settings from the manifest.
  `--via-kusto`/`--via-region` route the connection as in snapshot modes.
- `--output` is required because there is no data directory to default to.
- The repository, provider, model, `--max-rounds`, and `--review-rounds`
  options behave as in snapshot modes.
- `--intent`, `--intent-file`, `--dynamic`, and `--dynamic-file` are mutually
  exclusive.

The objective should name the resources and time window to investigate (for
example, a subscription, cluster or node pool name, and a timestamp); the agent
discovers the remaining identifiers itself.

### Query library

The queries that build snapshots live in
`tooling/hcpctl/pkg/snapshot/queries/<component>/<queryName>/`, each with a
`query.kql` Go template and a `README.md`. Dynamic mode adds declarative
metadata so the same queries can be found and run interactively:

- `queries/params.yaml` is the catalog of parameters shared by all queries
  (ResourceID, ClusterID, FullStartTime, ...). Each entry has a type (`string`,
  `datetime`, `int`), a hint telling the agent how to obtain the value, and
  optionally a `default` (another parameter), `derive` rules (a function applied
  to another parameter, such as `armSubscriptionID` of ResourceID), a `guide`,
  or `source: config` for values fixed by the CLI (Kusto cluster and
  databases). Parameter names are global by convention, so hints are written
  once.
- `queries/<component>/<queryName>/query.yaml` describes one query: a
  one-line description, target database, tags, the parameters it needs (by
  name, optionally with a local hint, type or `optional` override; parameters
  absent from the catalog are local to the query), `requireAnyOf` groups when
  any one of several parameters suffices, the parameters its results
  `provides` (from a result column, or a template over columns), and notes.
  Nothing requires a resource ID: a query over, say, Prometheus health declares
  only the parameters its template uses.
- `queries/README.md` is a short overview of parameter resolution and the
  dependency chains between queries (for example ClusterID comes from
  `clustersService/cid`), and `queries/guides/` explains the important
  identifiers. The overview is inlined into the agent's initial prompt.

The agent uses four tools backed by a per-investigation session:

- `list_queries [filter]`: queries with their description, the parameters
  they need, and whether the parameters known so far suffice to run them.
- `describe_query <name>`: a query's README, each parameter's current value or
  how to obtain it (including which query provides it), the values the query
  provides, and the KQL it would run now.
- `describe_param <name>`: a parameter's meaning, current value and origin,
  derivation, providers, users, and guide.
- `run_query <name> [params]`: renders the template, runs it, and writes the
  README, rendered KQL and full results to
  `results/NNN-<component>-<queryName>.md` in the workspace, in the same
  format as a snapshot's query output. The tool returns the rendered KQL, the
  first rows, and the file path.

The session resolves each parameter from, in order: CLI configuration, the
call's arguments, derivation from other parameters, a remembered value, a
default, or empty for optional parameters. Arguments are remembered across
calls, and when a query's results contain exactly one value for a parameter
it `provides`, that value is remembered too (several distinct values are
reported instead, so the agent picks one). A provided value records the
parameters it was computed from and is ignored once they change, so moving to
a different resource never reuses another resource's ClusterID. A missing
parameter produces an error listing the hints and the queries that provide it.

The library files themselves are also copied to `queries/` in the workspace
so the agent can search them.

Unit tests keep the metadata honest: every query has a `query.yaml`, its
declared parameters equal the template's variables, every catalog parameter is
used and its guide exists, `provides` match the values the snapshot gatherer
extracts, and rendering through the library equals the gatherer's rendering
for every query.

The prompts use a dedicated dynamic mode: they frame the work around the
supplied objective, never assume a snapshot or a failed test, and omit the
exemplar analyses. The validation, hydration, review, and output pipeline is
unchanged, except that log proofs are rejected (there are no gathered logs, so
log lines must be cited through Kusto proofs) and the rendered heading is the
model-authored title.

## Debugging with conversation.json

When the analysis produces unexpected results or the agent gets stuck in
validation loops, `conversation.json` is the primary debugging tool. It
contains the complete message history between the CLI and the agent, in the
same format the Copilot SDK uses internally.

The conversation is snapshotted after every successful agent turn. If you
interrupt the process with ctrl-C, the file will contain everything up to and
including the last completed turn.

## Analyzing Pull Request Jobs

When analyzing `pull-ci` jobs for a pull request, set `$AZURE_CONFIG_DIR` to
an `az` login for the Red Hat tenant, and provide `$AZURE_TOKEN_CREDENTIALS`
as `AzureCLICredentials`. The GitHub Copilot authentication mode will retain
whichever credentials are available for the `copilot` CLI, _not_ the Azure
credentials provided via environment variables for authentication to Kusto,
etc. This allows authentication to the Red Hat tenant for Kusto queries and
to the Microsoft tenant for the Copilot subscription.
