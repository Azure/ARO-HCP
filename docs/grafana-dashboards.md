# Grafana dashboards

Grafana is deployed using a Managed Grafana instance. Data is available via preconfigured Datasources.

## Managing Dashboards

There is a pipeline step to import dashboards. You need to create a `grafana-dashboards` folder in the service directory, This directory must be at the highest level in the service directory. Example [istio/grafana-dashboards](https://github.com/Azure/ARO-HCP/tree/main/istio/grafana-dashboards). The pipeline will create a folder in Grafana named `istio` then.
