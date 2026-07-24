---
name: gather-test-data
description: Gather failing e2e test data from Sippy for an ARO HCP environment and download artifacts.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

## What I Do

Run the `triage failing-tests` command to query Sippy for failing e2e test runs
in a given ARO HCP environment, print a summary of failing tests by frequency,
and download artifacts for each failing job run to a local directory.

## When to Use Me

Use this skill when the user wants to:
- find out which e2e tests are failing in an ARO HCP environment
- download test artifacts (logs, configs) for failing job runs
- get a summary of test failure rates over a time window

## Parameters

The user may provide these parameters directly in their message. Extract them
from the user's request. Only ask for a parameter if it is required and was not
provided.

| Parameter | Make Variable | Required | Default | Description |
|-----------|--------------|----------|---------|-------------|
| Environment | `ENVIRONMENT` | yes | none | Sippy environment to query. Must be one of: `aro-integration`, `aro-stage`, `aro-production`. |
| Since | `SINCE` | no | `168h` | How far back to look for failing tests, as a Go duration (e.g. `168h` for 7 days, `720h` for 30 days). Must be positive and at most 90 days. |
| Output directory | `OUTPUT_DIR` | yes | none | Local directory to write per-job-run artifacts into. |

## How to Run

Once you have the parameters, run `make run` in this skill's directory, passing
the values as Make variables:

```bash
make run ENVIRONMENT=<environment> SINCE=<since> OUTPUT_DIR=<output_dir>
```

The working directory must be `tooling/triage/skills/gather-test-data`.

`SINCE` can be omitted to use the default of `168h`.

## Example

```bash
make run ENVIRONMENT=aro-integration SINCE=168h OUTPUT_DIR=/tmp/triage-artifacts
```
