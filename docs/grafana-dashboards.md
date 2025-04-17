# Grafana dashboards

Grafana is deployed using a Managed Grafana instance. Data is available via preconfigured Datasources.

## Managing Dashboards

There is a pipeline step to import dashboards. You need to create a `grafana-dashboards` folder in the service directory.

This directory must be added to the `observability/observability.yaml` file.

```yaml
grafana-dashboards:
  dashboardFolders:
  - name: istio
    path: ../istio/grafana-dashboards
```

The pipeline will create a folder in Grafana named `istio` and put the dashboards in grafan-dashboards folder there.
