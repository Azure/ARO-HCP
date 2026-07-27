# Snapshot Gathering and Analysis

The `hcpctl snapshot` commands gather structured diagnostic data for failed
E2E tests and run LLM-driven root cause analysis on the results.

The workflow has two steps:

1. **Gather** — fetch Prow job artifacts, test logs, and Kusto query results
   into a local directory.
2. **Analyze** — send the gathered data to a Copilot agent that produces a
   structured causal chain explaining why the test failed.

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

The command writes three files to the output directory (defaults to the data
directory, overridable with `--output`):

- `analysis.json` — the fully hydrated causal chain as structured JSON.
- `analysis.md` — the chain rendered as a readable markdown document.
- `conversation.json` — the full conversation history with the agent,
  including all prompts, responses, and tool calls.

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
