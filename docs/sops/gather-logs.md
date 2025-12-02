# Must-Gather Commands Documentation

This document provides comprehensive documentation for the `hcpctl must-gather` commands, specifically the `legacy-query` and `clean` subcommands.

## Overview

The must-gather commands are designed to collect and process diagnostic data from Azure Data Explorer (Kusto) clusters for ARO-HCP (Azure Red Hat OpenShift - Hosted Control Planes) environments. These commands help with troubleshooting and analysis by gathering logs and cleaning sensitive information.

## Commands

### 0. query

The `query` command is supported in the Kusto instances owned by SLSRE, currently this can be used with dev and int clusters. Prod is work in progress. The difference is simply to use `must-gather query` instead of `must-gather legacy-query`, all the rest works the same.

### 1. legacy-query

The `legacy-query` command executes preconfigured queries against Azure Data Explorer clusters using the `akskubesystem` table. This is legacy, cause it uses the ARO Classic table schema and is planned to replace with HCP specific schema/cli in the future.

*Important:*, when you want to gather data for integrated dev, use the `must-gather query` command instead.

#### Purpose
- Execute default queries against Azure Data Explorer (Kusto)
- Collect service logs for ARO-HCP services and hosted control planes
- Generate structured output for analysis

#### Required Parameters
- `--kusto`: Azure Data Explorer cluster name, [database list](https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/doc/monitoring/kusto/kusto-database-list)
- `--region`: Azure Data Explorer cluster region  
- `--subscription-id`: Azure subscription ID
- `--resource-group`: Azure resource group name

#### Optional Parameters
- `--output-path`: Path to write output files (default: auto-generated timestamp-based directory)
- `--query-timeout`: Query execution timeout (default: 5 minutes, range: 30 seconds to 30 minutes)
- `--skip-hcp-logs`: Skip hosted control plane logs collection
- `--timestamp-min`: Minimum timestamp for data collection (default: 24 hours ago)
- `--timestamp-max`: Maximum timestamp for data collection (default: current time)
- `--limit`: Limit number of results returned


#### Authentication Requirements

The commands use standard Azure authentication. Users must authenticate using the Azure CLI before running the commands:

```bash
# Authenticate with Azure
az login

# Verify authentication
az account show

# Set the correct subscription if needed
az account set --subscription "your-subscription-id"
```

#### Usage Examples

**Basic usage with required parameters:**
```bash
hcpctl must-gather legacy-query \
  --kusto my-kusto-cluster \
  --region eastus \
  --subscription-id 12345678-1234-1234-1234-123456789012 \
  --resource-group my-resource-group
```

**With custom output path and time range:**
```bash
hcpctl must-gather legacy-query \
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
hcpctl must-gather legacy-query \
  --kusto my-kusto-cluster \
  --region eastus \
  --subscription-id 12345678-1234-1234-1234-123456789012 \
  --resource-group my-resource-group \
  --skip-hcp-logs
```

**With custom timeout and result limit:**
```bash
hcpctl must-gather legacy-query \
  --kusto my-kusto-cluster \
  --region eastus \
  --subscription-id 12345678-1234-1234-1234-123456789012 \
  --resource-group my-resource-group \
  --query-timeout 10m \
  --limit 1000
```

#### Output Structure
The command creates the following directory structure:
```
<output-path>/
├── service/                    # Service logs directory
│   ├── containerLogs.json
│   ├── frontendContainerLogs.json
│   └── backendContainerLogs.json
├── host-control-plane/        # Hosted control plane logs (if not skipped)
│   └── customerLogs.json
└── options.json              # Query options used
```

#### Handling large data

Kusto has limits for what a query can return, in order to overcome these, you can check the `json` files created. These contain information on the datasize queried. You can then use the `limit` and `timestamp` parameters to reduce the number of log rows gathered. These filters are applied per query.

### 2. clean

The `clean` command processes must-gather data to remove sensitive information using the [openshift/must-gather-clean](https://github.com/openshift/must-gather-clean) tool.

*Important:* If you are cleaning data from MSFT environments, this tool *MUST* be run using the configuration in `sdp-pipelines`!

#### must-gather-clean Binary Installation

The `must-gather-clean` binary is available from the [openshift/must-gather-clean releases](https://github.com/openshift/must-gather-clean/releases) page.

#### Required Parameters
- `--path-to-clean`: Path to the must-gather data to clean
- `--service-config-path`: Path to ARO-HCP Service Configuration file (points to `config` directory containing `config.yaml`)
- `--must-gather-clean-binary`: Path to the must-gather-clean binary
- `--cleaned-output-path`: Path where cleaned output will be written

#### Optional Parameters
- `--clean-config-path`: Path to custom must-gather-clean configuration file

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

#### Default Clean Configuration
When no custom configuration is provided, the default config can be found here [default_config.json](https://github.com/Azure/ARO-HCP/blob/main/tooling/hcpctl/cmd/must-gather/default_config.json)


#### Process Flow
1. **Configuration Loading**: Loads default or custom must-gather-clean configuration
2. **Pattern Discovery**: Scans service configuration files for UUIDs and other sensitive patterns
3. **Configuration Extension**: Adds discovered patterns to the clean configuration
4. **Configuration Persistence**: Saves the final configuration to a temporary file
5. **Clean Execution**: Runs the must-gather-clean binary with the generated configuration
6. **Output Generation**: Creates cleaned output in the specified directory



