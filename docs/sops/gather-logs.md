# Must-Gather Commands Documentation

This document provides comprehensive documentation for the `hcpctl must-gather` commands, specifically the `legacy-query` and `clean` subcommands.

## Overview

The must-gather commands are designed to collect and process diagnostic data from Azure Data Explorer (Kusto) clusters for ARO-HCP (Azure Red Hat OpenShift - Hosted Control Planes) environments. These commands help with troubleshooting and analysis by gathering logs and cleaning sensitive information.

## Commands

### 0. query

The `query` command is supported in the Kusto instances owned by SLSRE. See this Link for an up to date list of clusters and URLs:  [hcp/components-and-architecture/kusto](https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/components-and-architecture/kusto)

### 1. query

Use query to fetch data for a specific HCP.

#### Usage Examples

**Basic usage with required parameters:**
```bash
hcpctl must-gather query \
  --kusto my-kusto-cluster \
  --region eastus \
  --subscription-id 12345678-1234-1234-1234-123456789012 \
  --resource-group my-resource-group
```

**With custom output path and time range:**
```bash
hcpctl must-gather query \
  --kusto my-kusto-cluster \
  --region eastus \
  --subscription-id 12345678-1234-1234-1234-123456789012 \
  --resource-group my-resource-group \
  --output-path ./my-diagnostics \
  --timestamp-min "2024-01-01T00:00:00Z" \
  --timestamp-max "2024-01-02T00:00:00Z"
```

**Skip hosted control plane logs:**
```bash
hcpctl must-gather query \
  --kusto my-kusto-cluster \
  --region eastus \
  --subscription-id 12345678-1234-1234-1234-123456789012 \
  --resource-group my-resource-group \
  --skip-hcp-logs
```

**With custom timeout and result limit:**
```bash
hcpctl must-gather query \
  --kusto my-kusto-cluster \
  --region eastus \
  --subscription-id 12345678-1234-1234-1234-123456789012 \
  --resource-group my-resource-group \
  --query-timeout 10m \
  --limit 1000
```

#### Handling large data

Kusto has limits for what a query can return, in order to overcome these, you can check the `json` files created. These contain information on the datasize queried. You can then use the `limit` and `timestamp` parameters to reduce the number of log rows gathered. These filters are applied per query.

Alternatively you can disable limits, by setting `limit` to `-1`. Caution watch the query size, if it is taking very long (i.e. more than five minutes) reduce the window by setting a limit and/or timestamps.

### 2. clean

The `clean` command processes must-gather data to remove sensitive information using the [openshift/must-gather-clean](https://github.com/openshift/must-gather-clean) tool.

*Important:* If you are cleaning data from MSFT environments, this tool *MUST* be run using the configuration in `sdp-pipelines`!

#### must-gather-clean Binary Installation

The `must-gather-clean` binary is available from the [openshift/must-gather-clean releases](https://github.com/openshift/must-gather-clean/releases) page.

#### Usage Examples

**Basic usage with required parameters:**
```bash
hcpctl must-gather clean \
  --path-to-clean ./must-gather-20240101-120000 \
  --service-config-path /home/jboll/workspace/opensource/ARO-HCP/config \
  --must-gather-clean-binary /usr/local/bin/must-gather-clean \
  --cleaned-output-path ./cleaned-output
```

**With custom clean configuration:**
```bash
hcpctl must-gather clean \
  --path-to-clean ./must-gather-20240101-120000 \
  --service-config-path /home/jboll/workspace/opensource/ARO-HCP/config \
  --must-gather-clean-binary /usr/local/bin/must-gather-clean \
  --cleaned-output-path ./cleaned-output \
  --clean-config-path ./custom-clean-config.json
```

### 3. query-infra

This command fetches all service logs for a given cluster. This can produce quite a lot of data and usually you should use the above `query` command instead.

#### Usage Examples

```
hcpctl must-gather query-infra \
  --kusto hcp-dev-us-2 \
  --region eastus2 \
  --infra-cluster prow-j1231233-mgmt-1 \
  --infra-cluster prow-j3453453-svc 
```
