---
name: gather-job-data
description: Download failing e2e test artifacts for a single Prow job run given its URL.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

## What I Do

Run the `triage job-failures` command to download test artifacts for a single
Prow job run. The command fetches test results from GCS, writes an aggregated
`tests.json`, and creates per-test directories containing `output.log`,
`error.log`, and `metadata.json` for each failed test.

## When to Use Me

Use this skill when the user wants to:
- download test artifacts for a specific Prow job run (identified by URL)
- investigate failures from a single job rather than querying Sippy for many runs
- get test logs and metadata for a known failing Prow job

## Parameters

The user may provide these parameters directly in their message. Extract them
from the user's request. Only ask for a parameter if it is required and was not
provided.

| Parameter | Make Variable | Required | Default | Description |
|-----------|--------------|----------|---------|-------------|
| URL | `URL` | yes | none | Prow job URL. Supports both periodic/postsubmit URLs (e.g. `https://prow.ci.openshift.org/view/gs/test-platform-results/logs/<job>/<prow-id>`) and PR (presubmit) URLs (e.g. `https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/<org_repo>/<pr>/<job>/<prow-id>`). |
| Output directory | `OUTPUT_DIR` | yes | none | Local directory to write per-job-run artifacts into. |

## How to Run

Once you have the parameters, run `make run` in this skill's directory, passing
the values as Make variables:

```bash
make run URL=<prow-job-url> OUTPUT_DIR=<output_dir>
```

The working directory must be `tooling/triage/skills/gather-job-data`.

## Example

```bash
make run URL=https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-Azure-ARO-HCP-main-aro-hcp-e2e-parallel/1234567890 OUTPUT_DIR=/tmp/triage-artifacts
```

## Output

The command writes the following directory structure under `OUTPUT_DIR`:

```
<output-dir>/<job-name>/<prow-id>/
  config.yaml          # Filtered Kusto connection info (region, cluster, databases)
  tests.json           # All test results aggregated
  <sanitized-test-name>/
    metadata.json      # Test metadata (minus output/error)
    output.log         # Test stdout
    error.log          # Test stderr
```

Only failed tests get per-test directories. The `config.yaml` and per-test
artifacts can then be used with the `triage-test` skill to query Kusto logs
for root-cause analysis.
