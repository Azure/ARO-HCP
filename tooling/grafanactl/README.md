# grafanactl

A command-line utility for managing Azure Managed Grafana instances, used in the ARO HCP context.

## Overview

grafanactl helps maintain Azure Managed Grafana instances by providing tools to:
- List all datasources in a Grafana instance
- Add Azure Monitor Workspace datasources to Grafana
- Remove orphaned Azure Monitor Workspace integrations
- Clean up stale datasources pointing to deleted resources

This tool is particularly useful when Azure Monitor Workspaces (Prometheus instances) are removed from your infrastructure but their references remain in Grafana, creating stale integrations.

## Installation

Build the tool from source:

```bash
go build -o grafanactl .
```

## Authentication

grafanactl uses Azure Active Directory authentication. Ensure you are logged into Azure CLI:

```bash
az login
```

The tool will use the same authentication context as other Azure CLI tools.

## Usage

### Common Flags

All commands require these basic parameters:

- `--subscription` - Azure subscription ID
- `--resource-group` - Azure resource group name
- `--grafana-name` - Azure Managed Grafana instance name
- `--output` - Output format: `table` (default) or `json`
- `-v, --verbosity` - Set logging verbosity level (0-10)

Commands that modify resources also support:

- `--dry-run` - Perform a dry run without making changes (available as flag for `modify` commands; for `clean` commands use `DRY_RUN` environment variable)

### Environment Variables

All command-line options can also be set via environment variables to simplify usage and scripting:

- `GRAFANA_SUBSCRIPTION` - Azure subscription ID (alternative to `--subscription`)
- `GRAFANA_RESOURCE_GROUP` - Azure resource group name (alternative to `--resource-group`)
- `GRAFANA_NAME` - Azure Managed Grafana instance name (alternative to `--grafana-name`)
- `GRAFANA_RESOURCE_ID` - Complete Azure Grafana resource ID (automatically parses subscription, resource group, and name)
- `GRAFANA_OUTPUT` - Output format: `table` or `json` (alternative to `--output`)
- `MONITOR_WORKSPACE_ID` - Azure Monitor Workspace resource ID (alternative to `--monitor-workspace-id`)
- `DRY_RUN` - Set to `true` or `false` to enable/disable dry-run mode (alternative to `--dry-run`)

Environment variables take precedence over default values but can be overridden by explicit command-line flags.

**Note**: There is currently an inconsistency in the codebase where the flag help text mentions `GRAFANACTL_*` environment variable names, but the actual implementation uses the `GRAFANA_*`, `MONITOR_WORKSPACE_ID`, and `DRY_RUN` variable names listed above. Use the environment variable names documented in this section for reliable functionality.

### Configuration Priority

Configuration values are resolved in this order (later values override earlier ones):

1. Default values
2. Environment variables
3. Command-line flags

### Configuration Examples

**Using command-line flags only:**

```bash
grafanactl list datasources \
  --subscription "12345678-1234-1234-1234-123456789abc" \
  --resource-group "my-rg" \
  --grafana-name "my-grafana"
```

**Using environment variables for common settings:**

```bash
export GRAFANA_SUBSCRIPTION="12345678-1234-1234-1234-123456789abc"
export GRAFANA_RESOURCE_GROUP="my-rg"
export GRAFANA_NAME="my-grafana"

# Now commands are much simpler
grafanactl list datasources
DRY_RUN="true" grafanactl clean datasources
```

**Using complete resource ID (most convenient):**

```bash
export GRAFANA_RESOURCE_ID="/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/my-rg/providers/Microsoft.Dashboard/grafana/my-grafana"

# All commands work without additional parameters
grafanactl list datasources
grafanactl modify datasource add --monitor-workspace-id "/subscriptions/.../providers/Microsoft.Monitor/accounts/my-workspace"
```

**Example using environment variables:**

```bash
export GRAFANA_SUBSCRIPTION="your-subscription-id"
export GRAFANA_RESOURCE_GROUP="your-resource-group"
export GRAFANA_NAME="your-grafana-instance"
export GRAFANA_OUTPUT="json"

# Now you can run commands without specifying common flags
grafanactl list datasources

# Alternative: Use the complete resource ID (automatically extracts components)
export GRAFANA_RESOURCE_ID="/subscriptions/your-subscription/resourceGroups/your-rg/providers/Microsoft.Dashboard/grafana/your-grafana-instance"
grafanactl list datasources

# Add datasource with environment variable
export MONITOR_WORKSPACE_ID="/subscriptions/your-subscription/resourceGroups/your-rg/providers/Microsoft.Monitor/accounts/your-workspace"
export DRY_RUN="true"
grafanactl modify datasource add
```

### List Commands

#### List Datasources

Display all datasources configured in your Grafana instance:

```bash
grafanactl list datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance"
```

Output formats:
- **Table format** (default): Human-readable table with ID, name, type, and URL
- **JSON format**: Machine-readable JSON for scripting and integration

```bash
# JSON output for scripting
grafanactl list datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --output json
```

### Modify Commands

Modify commands help manage your Grafana instance by adding or updating resources.

#### Add Datasource (Azure Monitor Workspace Integration)

Add an Azure Monitor Workspace as a datasource to your Azure Managed Grafana instance. This integrates the workspace with Grafana and creates the necessary datasource configuration:

```bash
# Preview changes (dry-run)
grafanactl modify datasource add \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --monitor-workspace-id "/subscriptions/your-subscription/resourceGroups/your-rg/providers/Microsoft.Monitor/accounts/your-workspace" \
  --dry-run

# Apply changes
grafanactl modify datasource add \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --monitor-workspace-id "/subscriptions/your-subscription/resourceGroups/your-rg/providers/Microsoft.Monitor/accounts/your-workspace"
```

**Important notes:**
- The command will automatically build a correct list of all existing Azure Monitor Workspace integrations and add the new one
- If the workspace is already integrated, the command will do nothing
- Only valid Azure Monitor Workspaces that still exist will be included in the final integration list
- The `--monitor-workspace-id` must be the full Azure resource ID of the Azure Monitor Workspace

### Clean Commands

Clean commands help maintain your Grafana instance by removing stale references and orphaned resources.

#### Clean Datasources (Azure Monitor Workspace Integrations)

Remove orphaned Azure Monitor Workspace integrations from the Grafana resource. This cleans up references to Azure Monitor Workspaces that no longer exist:

```bash
# Preview changes (dry-run) - note: clean commands only support dry-run via environment variable
DRY_RUN="true" grafanactl clean datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance"

# Apply changes
grafanactl clean datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance"
```

#### Fixup Datasources

Delete orphaned datasources within the Grafana instance itself. This removes any Managed Prometheus datasources that are no longer valid:

```bash
# Preview changes (dry-run) - note: clean commands only support dry-run via environment variable
DRY_RUN="true" grafanactl clean fixup-datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance"

# Apply changes
grafanactl clean fixup-datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance"
```

## Error Handling

- The tool includes retry logic for transient Azure API failures
- Use `--verbosity` flag to increase logging detail for troubleshooting
- Always use `--dry-run` first to preview changes before applying them

