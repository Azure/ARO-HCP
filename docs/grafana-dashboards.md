# Grafana dashboards

Grafana is deployed using a Managed Grafana instance. Data is available via preconfigured Datasources.

## Prerequisites

In staging, integration, and production environments, public network access to the Azure Managed Grafana instance may be disabled. If so, you must be connected to the **MSFT Corp VPN** to access Grafana. Dev environment Grafana instances are publicly accessible. See [Grafana VPN Access](sops/grafana-vpn-access.md) for troubleshooting.

## Managing Dashboards

There is a pipeline step to import dashboards. You need to create a `grafana-dashboards` folder in the ARO-HCP repo. This dashboard *MUST* be within the `observability/grafana-dashboards` folder, cause only observability is packaged into the EV2 artifact.

This directory must be added to the `observability/observability.yaml` file.

```yaml
grafana-dashboards:
  dashboardFolders:
  - name: istio
    path: ./grafana-dashboards/istio
```

The pipeline will create a folder in Grafana named `istio` and put the dashboards in grafana-dashboards folder there.

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
- `az` CLI, logged in with `az login`. You need access to the dev subscription.
- `curl` on the host.
- A personal dev environment, so the AMWs exist. Run `make personal-dev-env`. See [`docs/personal-dev.md`](./personal-dev.md).

### Initial setup

1. Log in to Azure:
   ```bash
   az login
   ```
2. Start Grafana (default `DEPLOY_ENV=pers`):
   ```bash
   make local-grafana-start
   ```
3. Open `http://localhost:3000`. Anonymous access is read-only. To edit a dashboard, log in as `admin` / `admin`.

> [!NOTE]
> The AMW access token lasts about 1 hour. To refresh it, run `make local-grafana-start`
> again. The command re-generates the datasource configuration and restarts the
> container.

### Iterating on dashboards

1. Add or edit dashboard JSON under `observability/grafana-dashboards/<folder>/`.
2. If you add a new folder, register it in `observability/observability.yaml` under `dashboardFolders`. See [Managing Dashboards](#managing-dashboards) above. The same file drives both local provisioning and production import.
3. Restart with `make local-grafana-start` to load the new folder.
4. Set datasource variable regexes on your dashboard. See [Dashboards datasources and other variables](#dashboards-datasources-and-other-variables) above.
5. Edit the dashboard in the UI as `admin`. Then export the JSON model and save it into the folder.

> [!IMPORTANT]
> The repo dashboards directory mounts into the container as read-only. Grafana does not write your UI edits back to disk. You must export the JSON model and commit it yourself.

### Committing changes

1. Validate the dashboard in local Grafana.
2. Export the dashboard JSON model from the UI.
3. Save the JSON into the correct folder under `observability/grafana-dashboards/`.
4. Commit the JSON and open a PR.

### Configuration

Set these environment variables to override the defaults:

| Variable | Default | Purpose |
|----------|---------|---------|
| `DEPLOY_ENV` | `pers` | Target dev environment. |
| `GRAFANA_PORT` | `3000` | Local port for Grafana. |
| `GRAFANA_VERSION` | `12.4.9` | Grafana image tag. |
| `CONTAINER_NAME` | `aro-hcp-grafana` | Container name. |
| `AZURE_CLIENT_ID` | (unset) | Service principal appId for the optional Azure Monitor datasource. |
| `AZURE_CLIENT_SECRET` | (unset) | Service principal secret for the optional Azure Monitor datasource. |
| `AZURE_TENANT_ID` | from `az account` | Tenant for the Azure Monitor datasource. |
| `AZURE_SUBSCRIPTION_ID` | from `az account` | Subscription for the Azure Monitor datasource. |

The script adds an "Azure Monitor" datasource only when you set both `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`. This datasource needs a service principal with the `Monitoring Reader` role. Run `make local-grafana-help` for exact instructions to create the service principal.

### Troubleshooting

**AMW connectivity fails.** The Azure Monitor Workspaces must exist. Run `make personal-dev-env` to create your personal dev region. Confirm that `az` is logged into the "ARO Hosted Control Planes (EA Subscription 1)" subscription. The `start` step runs a Prometheus `up` query and fails early with an actionable message.

**The optional Azure Monitor datasource does not appear.** You must set both `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`, then re-run `make local-grafana-start`.
