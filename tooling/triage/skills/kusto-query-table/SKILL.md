---
name: kusto-query-table
description: Render a Kusto v2 JSON response as a markdown table.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

## What I Do

Parse a raw Kusto v2 REST API JSON response file and render the PrimaryResult
data as a GitHub-flavored markdown table with column headers and row data.
Objects and arrays in cells are JSON-encoded; primitive values are rendered as-is.

## When to Use Me

Use this skill when you have a raw Kusto v2 JSON response file (e.g. an
`output.json` produced by `kusto-query` or `trace-request`) and want to view
it as a human-readable markdown table.

## Parameters

Only ask for a parameter if it is required and was not provided by the user.

| Parameter | Make Variable | Required | Description |
|-----------|--------------|----------|-------------|
| Input file | `INPUT_FILE` | yes | Path to a file containing the raw Kusto v2 JSON response. |

## How to Derive Each Parameter

### Input file

The input file is a raw JSON response from the Kusto v2 REST API. These are
produced as `output.json` files by the `trace-request` command, or can be
captured from any Kusto v2 `/v2/rest/query` response.

## How to Run

Once the parameter is determined, run `make run` in this skill's directory:

```bash
make run INPUT_FILE=<path-to-output.json>
```

The working directory must be `tooling/triage/skills/kusto-query-table`.

## Output

The markdown table is printed to stdout. It contains column headers from the
Kusto response and one row per result row.
