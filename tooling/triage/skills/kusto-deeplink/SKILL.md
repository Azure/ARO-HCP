---
name: kusto-deeplink
description: Generate an Azure Data Explorer deep-link URL for a templated KQL query.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

## What I Do

Using a Kusto query template file and the provided parameters, render a KQL script
and produce an Azure Data Explorer deep-link URL that opens the query in the ADX
web UI. The URL encodes the rendered query with gzip and base64.

## When to Use Me

Use this skill when we need to generate a clickable link to a Kusto query rather
than executing the query directly.

## Parameters

Only ask for a parameter if it is required and was not provided by the user.

| Parameter | Make Variable | Required | Description |
|-----------|--------------|----------|-------------|
| Config file | `CONFIG_FILE` | yes | Path to a `config.yaml` containing Kusto connection details. The Makefile extracts cluster name, region, and database names from this file automatically. |
| Start time | `START_TIME` | yes | Start of the time window to query, in RFC3339 format (e.g. `2026-04-06T02:03:13Z`). |
| End time | `END_TIME` | yes | End of the time window to query, in RFC3339 format (e.g. `2026-04-06T02:33:13Z`). |
| Resource group | `RESOURCE_GROUP` | yes | Azure resource group to filter logs by. |
| KQL file | `KQL_FILE` | yes | Path to a KQL Go template file to render. |
| Extra variables | `EXTRA_VARS` | no | Additional key=value pairs for the KQL template, available as `{{ .Extra.<key> }}`. Pass as a space-separated list of `key=value` items. |

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
metadata, user input, or any other source.

### Resource group

Provide the Azure resource group to scope the query to. This may come from test
logs, user input, or any other source.

### KQL file

Provide the KQL script file to be encoded into the deep-link.

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

## How to Run

Once all parameters are determined, run `make run` in this skill's directory:

```bash
make run \
  CONFIG_FILE=<path-to-config.yaml> \
  START_TIME=<rfc3339-start> \
  END_TIME=<rfc3339-end> \
  RESOURCE_GROUP=<resource-group> \
  KQL_FILE=<kql-file> \
  EXTRA_VARS="<key1>=<value1> <key2>=<value2>"
```

The working directory must be `tooling/triage/skills/kusto-deeplink`.

## Output

A Markdown block is printed to stdout containing a clickable deep-link and the raw
rendered KQL query, formatted for easy sharing:

````
Open in [ADX Web](<https://dataexplorer.azure.com/clusters/<cluster-host>/databases/<database>?query=<encoded>>)

```kql
<raw KQL query>
```
````

Copy the output into any Markdown-capable surface (GitHub issues, pull requests,
chat) to share a formatted query with a one-click link to the ADX web UI.
