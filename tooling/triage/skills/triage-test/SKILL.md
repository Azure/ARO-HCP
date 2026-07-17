---
name: triage-test
description: Root-cause the failure in an end-to-end test.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

## What I Do

Authoritatively determine the reason for an end-to-end test failure.

Starting with a clear articulation of what the test was trying to achieve and
what failed, drill into the failure, recursively asking 'why?' to get a deeper
understanding of the root cause for each failure, at every level of the stack.

Output an ANALYSIS.md which iteratively makes statements of fact in response to
these 'why?' questions and provides direct proof that a reader could use to
reproduce the findings.

Never speculate, always prefer to admit that the answer to a 'why?' question is
unclear or that more data is necessary to understand the issue.

## When to Use Me

Use this skill when the user wants to:
- investigate why a specific e2e test failed

## Parameters

The user provides:
- the path to a failing test's artifact directory.
- the path to the root of the following repositories:
  - ARO-HCP
  - maestro
  - clusters-service

Query the user for any parameter that is not specified and cannot be inferred.

## Expected Output

Create a file ANALYSIS.md which provides:

1. A link to the failing job.
2. A concise statement of what the test was attempting to do, the proximal failure it
   encountered and what should have happened instead. What client and what server are involved?
3. Iteratively, a list of statements of fact that attempt to explain "why?" the previous
   statement in the list happened - the first such item attempts to explain the test's error.
   a. be painfully precise - if the client involved saw a specific error code, show the
      server logs that sent it before moving on to figure out why the server had an error.
      Always read the server's source code to understand the context for an error message.
   b. for every substantive claim in a statement, prove without a doubt that the claim is
       true with a Kusto query deep-link and the logs returned by the query, formatted verbatim
       as they are returned from Kusto, in a Markdown table. Use the kusto-query-table skill to
       render raw Kusto JSON output as a Markdown table. Prune the output of the Kusto query
      with `where` clauses or time bounds to make the output as concise as possible.
4. When a "why?" cannot be definitively proven, STOP and declare as such - do NOT theorize,
   make claims without proof, etc. Provide some insights into what other logs would have been
   necessary to continue.
5. Make clear suggestions for improvement:
   a. could any of the test code be improved to make the test more resilient?
   b. are any of the server processes not correctly self-healing or eventually consistent?
   c. are any components emitting malformed logs, too many logs, hard-to-parse logs, which
      could be improved to make debugging easier?
   d. what anomalies occurred during this test that should have been alerted on, what did
      we discover during the root-cause analysis that should not have been a surprise?

### Providing Adequate Proof

A root-cause analysis is not valuable if every single step cannot be independently verified.

Take great pains to use Kusto queries as proof in every conceivable place. If logs are
being used from the context window to come to a conclusion, craft a Kusto query that
minimally captures exactly the subset of the logs that are being used and provide that
query in the analysis document.

Ensure that the proof is a core part of the prose - follow every single claim with the
proof for it, using the deep-link tool to fill in the header, ensuring the analysis document
contains the deep link *and* the KQL Kusto query that was run, and providing the verbatim
output from Kusto in a table for human review. Use the kusto-query-table skill to convert
raw Kusto JSON responses into Markdown tables for embedding in the analysis document.

## Directions

Read the `error.log` in the test output directory to get an idea of the proximal
failure for the test case. Use the `output.log` in the test artifact directory for
more context on what the test was doing up until the failure.

Read the test's source code - found in `test/e2e/` relative to the root of the ARO-HCP
repository - to understand what test's intent.

If encountering error messages from the ARO HCP resource provider, consider searching
for them in `frontend/` or `backend/` directories relative to the ARO-HCP repository
to understand what the server means.

If encountering error messages from the OpenShift control plane, search the `openshift`
GitHub organization for error strings. If more context is necessary.

Keep in mind that context deadline exceeded, time-outs, _etc_, are simply symptoms
of underlying problems. Your goal is to determine the *root cause* - for each statement
of fact about the failure, ask "why?" and attempt to use Kusto queries to find data
that support an answer.

Review the content in the `references` sub-directory for an overview of the system, which
will help guide debugging sessions.

Review the queries in the `queries` sub-directory, reading the Markdown files for
queries that seem relevant. In order to execute one, set `KQL_FILE` to the `*.kql`
path matching the Markdown file (for instance, `frontend.md` matches `frontend.kql`).
Use the kusto-query skill to run the query, the kusto-deeplink skill to generate a
deep link to the query for the analysis document, and the kusto-query-table skill to
render the raw Kusto JSON output as a Markdown table. Ensure that queries are as minimal
as possible - for the smallest time period, filtered to the objects that matter, only
pertaining to the correlation ID for the single request that failed, etc.

Start by identifying the "core" mutating query that went wrong during the test, and use
the trace-request skill to grab the associated logs, state snapshots, and pod events to
determine, for each component in the stack:
- were there any anomalies in the handling of state, as seen in conditions or logs?
- do the state snapshots show any irregularities?
- did the component correctly forward state to the next part of the stack?
- do any events show issues with running the component at the time?

Review Kusto Table definitions in `dev-infrastructure/modules/logs/kusto/tables/` for
available tables, their schemas and ingest mappings.

When a server is acting strangely, remember to check the Kubernetes Events tables to see
if the pod was having any anomalous events during that time.

## Querying Kusto

Extract the `kusto-query` parameters from the artifacts as described below. Only ask for a
parameter if it cannot be derived and was not provided by the user.

| Parameter | Make Variable | Required | Description |
|-----------|--------------|----------|-------------|
| Config file | `CONFIG_FILE` | yes | Path to the `config.yaml` in the job run directory (parent of the test directory). The Makefile extracts cluster name, region, and database names from this file automatically. |
| Test metadata file | `TEST_METADATA_FILE` | yes | Path to the `metadata.json` in the test artifact directory. The Makefile extracts start and end times from this file automatically. |
| Resource group | `RESOURCE_GROUP` | yes | Azure resource group for the cluster under test. |
| KQL file | `KQL_FILE` | yes | Path to a KQL Go template file to execute. |
| Extra variables | `EXTRA_VARS` | no | Additional key=value pairs for the KQL template, available as `{{ .Extra.<key> }}`. Pass as a space-separated list of `key=value` items. |

### How to Derive Each Parameter

The test artifact directory is a child of a job run directory produced by the
`gather-test-data` skill. The directory layout is:

```
<output-dir>/<job-name>/<prow-id>/
  config.yaml
  tests.json
  <sanitized-test-name>/
    metadata.json
    output.log
    error.log
```

The user provides the path to the `<sanitized-test-name>/` directory.
The `config.yaml` file lives in the parent directory
(`<output-dir>/<job-name>/<prow-id>/`).

### Config file

Set `CONFIG_FILE` to the `config.yaml` in the parent directory of the test
artifact directory. For example, given test directory
`/tmp/artifacts/job/123/test-name/`, use `/tmp/artifacts/job/123/config.yaml`.

### Timestamps

A good starting point for `START_TIME` and `END_TIME` are the test start and end
times, taken from `.startTime` and `.endTime` in the test metadata file, found at
`metadata.json` relative to the test directory. You can extract these values and
format them in RFC3339 with this shell script:

```shell
START_TIME=$(date -u -d "$(jq -r .startTime "$TEST_METADATA_FILE")" +%Y-%m-%dT%H:%M:%SZ)
END_TIME=$(date -u -d "$(jq -r .endTime "$TEST_METADATA_FILE")" +%Y-%m-%dT%H:%M:%SZ)
```

### Resource group

The `output.log` file in the test artifact directory will contain a line like:

```
"ts"="2026-04-06 02:03:13.718605" "level"=0 "msg"="creating resource group" "resourceGroup"="private-keyvault-gxsj99"
```

Set `RESOURCE_GROUP` to the value from the `"resourceGroup"="<value>"` pair in this log line.

### KQL file

Choose a KQL file from the available queries, or write a new one as necessary.

### Extra variables

Some KQL templates require additional parameters beyond the standard set. Pass
these as `EXTRA_VARS` -- a space-separated list of `key=value` items. Each
pair becomes an `--extra-var key=value` flag and is available in the template
as `{{ .Extra.<key> }}`.

For example, if a KQL template references `{{ .Extra.clusterID }}`, pass:

```
EXTRA_VARS="clusterID=my-cluster-id"
```

Multiple values:

```
EXTRA_VARS="clusterID=my-cluster-id namespace=openshift-hcp"
```