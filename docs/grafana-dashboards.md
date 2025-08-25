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

### Dashboards datasources and other variables

It is highly recommended to set a regex filter on your datasource variable to ensure only datasources which are relevant to your dashboard are shown. Consider the following regexes for datasources:

| Regex                                      | Source     | Will show ...                        |
|--------------------------------------------|------------|--------------------------------------|
| `^Managed_Prometheus_hcps-.*$`             | datasource | Hypershift Control Plane datasources |
| `^Managed_Prometheus_services-.*$`         | datasource | Service datasources                  |
| `^.*-mgmt-\\d+$`                           | cluster    | Management clusters                  |
| `^.*-svc-\\d+$`                            | cluster    | Service clusters                     |
