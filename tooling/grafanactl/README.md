# grafanactl

A command-line utility for managing Azure Managed Grafana instances, used in the ARO HCP context.

## Overview

grafanactl helps maintain Azure Managed Grafana instances by providing tools to:
- List all datasources in a Grafana instance
- Remove orphaned Azure Monitor Workspace integrations
- Clean up stale datasources pointing to deleted resources
- Sync dashboards and folders from git to Grafana

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

### Clean Commands

Clean commands help maintain your Grafana instance by removing stale references and orphaned resources.

#### Clean Datasources (Azure Monitor Workspace Integrations)

Remove orphaned Azure Monitor Workspace integrations from the Grafana resource. This cleans up references to Azure Monitor Workspaces that no longer exist:

```bash
# Preview changes (dry-run)
grafanactl clean datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --dry-run

# Apply changes
grafanactl clean datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance"
```

#### Fixup Datasources

Delete orphaned datasources within the Grafana instance itself. This removes any Managed Prometheus datasources that are no longer valid:

```bash
# Preview changes (dry-run)
grafanactl clean fixup-datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --dry-run

# Apply changes
grafanactl clean fixup-datasources \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance"
```

### Sync Commands

Sync commands help keep your Grafana instance in sync with dashboard definitions stored in git.

#### Sync Dashboards

Synchronize dashboards and folders from a configuration file to Grafana. This will:
- Create folders that don't exist in Grafana
- Create or update dashboards from JSON files
- Delete stale dashboards that are no longer in git (excluding Azure managed folders)
- Validate dashboards and report errors/warnings

```bash
# Preview changes (dry-run)
grafanactl sync dashboards \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --config-file "../../observability/observability.yaml" \
  --dry-run

# Apply changes
grafanactl sync dashboards \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --config-file "../../observability/observability.yaml"
```

The config file (e.g., `observability.yaml`) defines:
- `grafana-dashboards.dashboardFolders`: List of folders with `name` and `path` to dashboard JSON files
- `grafana-dashboards.azureManagedFolders`: List of folder names managed by Azure (will not be modified)

## Error Handling

- The tool includes retry logic for transient Azure API failures
- Use `--verbosity` flag to increase logging detail for troubleshooting
- Always use `--dry-run` first to preview changes before applying them

