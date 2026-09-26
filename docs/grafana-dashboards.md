# Grafana dashboards

Grafana is deployed using a Managed Grafana instance. Data is available via preconfigured Datasources.

## Prerequisites

In staging, integration, and production environments, public network access to the Azure Managed Grafana instance may be disabled. If so, you must be connected to the **MSFT Corp VPN** to access Grafana. Dev environment Grafana instances are publicly accessible. See [Grafana VPN Access](sops/grafana-vpn-access.md) for troubleshooting.

## Managing Dashboards

There is a pipeline step to import dashboards. You need to create a `grafana-dashboards` folder in the ARO-HCP repo. This dashboard *MUST* be within the `observability/grafana-dashboards` folder, because only observability is packaged into the EV2 artifact.

This directory must be added to the `observability/observability.yaml` file.

```yaml
grafana-dashboards:
  dashboardFolders:
  - name: Some Folder Name
    path: ./grafana-dashboards/some-folder-name
```

The pipeline will create a folder in Grafana named `Some Folder Name`. Dashboards in `observability/grafana-dashboards/some-folder-name` will appear there.

### Dashboards datasources and other variables

It is highly recommended to set a regex filter on your datasource variable to ensure only datasources which are relevant to your dashboard are shown. Consider the following regexes for datasources:

| Regex                                           | Source     | Will show ...                        |
|-------------------------------------------------|------------|--------------------------------------|
| `^Managed_Prometheus_hcps-.*$`                  | datasource | Hypershift Control Plane datasources |
| `^Managed_Prometheus_services-.*$`              | datasource | Service datasources                  |
| `^.*-mgmt-\\d+$`                                | cluster    | Management clusters                  |
| `^.*-svc(?:-\\d+)?$`                            | cluster    | Service clusters                     |

## Local Development

The local development workflow runs a Grafana container on your workstation. The container connects to the Azure Monitor Workspace (AMW) Prometheus endpoints of your personal dev environment. You develop and test dashboards against live Managed Prometheus data before you open a PR.

The workflow is a script, `hack/local-grafana.sh`, wrapped by four make targets:

| Target | Action |
|--------|--------|
| `make local-grafana-start` | Start the container. Re-run to refresh the AMW token. |
| `make local-grafana-stop` | Stop and remove the container and generated state. |
| `make local-grafana-status` | Show the container status. |
| `make local-grafana-help` | Show usage and the full help text. |

### Prerequisites

- Podman or Docker.
- `az` CLI (logged in with `az login`), and access to the dev subscription.
- `az`, `curl`, and `yq` on the host.
- A personal dev environment, so the AMWs exist. See [`docs/personal-dev.md`](./personal-dev.md).

### Iterating on dashboards

1. Start the local Grafana container:
   ```bash
   make local-grafana-start
   ```
2. Open `http://localhost:3000` to access the container. The personal dev environment AMW access token lasts about 1 hour--to refresh it, run `make local-grafana-start` again. _Note that running this command will reset any in progress work. Make sure to export and save any in progress dashboard JSON files before refreshing._
3. Anonymous access is read-only. To edit a dashboard, login with email: `admin`, password: `admin`, then select "Skip".
4. If editing an existing dashboard:
  - select a Dashboard, and click "Edit", then make your edits.
5. To add a new dashboard:
  - go to the Dashboards page, click "New" > "New dashboard", and make your edits.
  - If you add a new folder, register it in `observability/observability.yaml` under `dashboardFolders`. See [Managing Dashboards](#managing-dashboards) above.
  - Set datasource regexes on your dashboard to pull data from certain datasources. See [Dashboards datasources and other variables](#dashboards-datasources-and-other-variables) above for common regex patterns.
6. When dashboard changes are ready to commit, click "Export" on the top right of the dashboard page > "Export as code" > "Copy to clipboard", then paste the json to the desired dashboard under `observability/grafana-dashboards/<folder>`.
7. Commit your changes and open a PR.

### Configuration

Set these environment variables to override the defaults:

| Variable | Default | Purpose |
|----------|---------|---------|
| `DEPLOY_ENV` | `pers` | Target dev environment. |
| `GRAFANA_PORT` | `3000` | Local port. |
| `GRAFANA_VERSION` | the Azure Managed Grafana instance version in `config.yaml` | Grafana image tag. |
| `CONTAINER_NAME` | `aro-hcp-grafana` | Container name. |
| `GRAFANA_ANONYMOUS_ENABLED` | `true` | Allow anonymous read-only (Viewer) access. |
| `GRAFANA_ADMIN_USER` | `admin` | Grafana admin username. |
| `GRAFANA_ADMIN_PASSWORD` | `admin` | Grafana admin password. |
| `AZURE_CLIENT_ID` | (unset) | Service principal appId for the "Azure Monitor" datasource. |
| `AZURE_CLIENT_SECRET` | (unset) | Service principal secret for the "Azure Monitor" datasource. |
| `AZURE_TENANT_ID` | tenant of the personal-dev svc subscription in `config.yaml` | Tenant for the "Azure Monitor" datasource. |
| `AZURE_SUBSCRIPTION_ID` | the personal-dev svc subscription in `config.yaml` | Subscription for the "Azure Monitor" datasource. |

Note: When `GRAFANA_VERSION` is unset, the script reads the version from the Azure Managed Grafana instance and fails if that lookup does not succeed. Explicitly set `GRAFANA_VERSION` in order to skip the lookup and reference a specific Grafana version.

Note: The script adds an "Azure Monitor" datasource only when you set both `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`. This datasource needs a service principal with the `Monitoring Reader` role. Run `make local-grafana-help` for exact instructions to create the service principal.

### Troubleshooting

- **AMW connectivity fails:** The Azure Monitor Workspaces must exist. Run `make personal-dev-env` to create your personal dev region. Confirm that `az` is logged into the "ARO Hosted Control Planes (EA Subscription 1)" subscription. The `start` step runs a Prometheus `up` query and fails early with an actionable message.
- **The optional "Azure Monitor" datasource does not appear:** You must set both `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`, then re-run `make local-grafana-start`. run `make local-grafana-help` for exact instructions to acquire these values.
