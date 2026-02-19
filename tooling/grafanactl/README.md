# grafanactl

A command-line utility for managing Azure Managed Grafana instances, used in the ARO HCP context.

### List Datasources

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

### Reconcile Datasource (Azure Monitor Workspace Integration)

Get all existing Azure Monitor Workspace Integrations and configure them in grafana. The command will automatically build an up to date list of all existing Azure Monitor Workspace integrations

```bash
# Preview changes (dry-run)
grafanactl modify datasource reconcile \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
  --dry-run

# Apply changes
grafanactl modify datasource reconcile \
  --subscription "your-subscription-id" \
  --resource-group "your-resource-group" \
  --grafana-name "your-grafana-instance" \
```

### Clean Datasources (Azure Monitor Workspace Integrations)

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

### Fixup Datasources

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
