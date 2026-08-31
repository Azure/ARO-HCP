# Agentic Hints for Debugging ARO HCP With Grafana

- IMPORTANT: this document is referenced by agentic workflows, DO NOT REMOVE IT.
- If any of the info below turns out not to be accurate, suggest to the user an update PR at the end of the session.

## Access Requirements

In staging, integration, and production environments, public network access to Grafana may be disabled. If so, the user must be connected to the **MSFT Corp VPN** to reach Grafana. If a Grafana URL is unreachable, prompt the user to verify VPN connectivity before further troubleshooting; see [Grafana VPN Access](../sops/grafana-vpn-access.md) for the full procedure. Dev environment Grafana instances are publicly accessible.

## Data Sources

### Obsolete Data Sources
- Data sources with 2 and 3 letter region suffixes are obsolete, do not use them for querying.
- Examples of obsolete data sources: services-ln, services-chn, hcps-ln, hcps-yt, etc.

### Data Sources Hints
- `hcps-*` data sources contain metrics from hosted control planes
- `services-*` data sources contain metrics from service and management clusters (underlay)
- the prod `*-eastus2euap` data sources are for the US canary region and match the `hcp-prod-usc` kusto cluster

## Alerts

- Alerts are defined using promql; they are not sent from on-cluster alertmanager, but from Azure Prometheus with bicep definitions in [dev-infrastructure/modules/metrics/rules/](../../dev-infrastructure/modules/metrics/rules/).

