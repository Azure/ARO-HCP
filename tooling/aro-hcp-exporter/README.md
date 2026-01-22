# ARO-HCP Exporter

A Prometheus resource exporter for ARO-HCP that exposes metrics via HTTP.

## Overview

This tool provides Prometheus metrics for ARO-HCP resources. It runs an HTTP server that exposes metrics in Prometheus format, which can be scraped by Prometheus or other monitoring systems.

## Building

```bash
make build
```

## Running

```bash
# Run with default settings (listens on :8080, metrics at /metrics)
./aro-hcp-exporter serve

# Customize listen address and metrics path
./aro-hcp-exporter serve --listen-address :9090 --metrics-path /metrics
```

Or use make:

```bash
make run
```

## Usage

The exporter provides a `serve` command that starts an HTTP server:

- `--listen-address`: Address to listen on (default: `:8080`)
- `--metrics-path`: Path to expose metrics on (default: `/metrics`)
- `--subscription-id`: Azure subscription ID (optional, defaults to `AZURE_SUBSCRIPTION_ID` env var)

## Metrics

The exporter exposes the following metrics:

### Core Metrics
- `aro_hcp_exporter_dummy_metric`: A dummy metric for testing the exporter

### Azure Public IP Metrics (when Azure credentials are configured)
- `aro_hcp_public_ip_count_by_service_tag`: Number of public IP addresses configured for each service tag
  - Labels: `service_tag`, `subscription_id`, `resource_group`, `location`
- `aro_hcp_public_ip_total`: Total number of public IP addresses per subscription
  - Labels: `subscription_id`

The service tag is extracted from Azure resource tags using common service identification patterns such as:
- `service`, `Service`
- `serviceName`, `ServiceName`
- `service-name`, `service_name`
- `component`, `Component`
- `app`, `App`, `application`, `Application`

If no service tag is found, the metric will use `"unknown"` as the service tag value.

## Example

```bash
# Start the exporter with just dummy metrics (no Azure subscription)
./aro-hcp-exporter serve

# Start the exporter with Azure public IP metrics
export AZURE_SUBSCRIPTION_ID="your-subscription-id"
./aro-hcp-exporter serve

# Or specify subscription ID via flag
./aro-hcp-exporter serve --subscription-id your-subscription-id

# In another terminal, query the metrics
curl http://localhost:8080/metrics
```

## Azure Authentication

To collect Azure public IP metrics, you need to authenticate with Azure. The exporter uses Azure Default Credential, which supports multiple authentication methods:

1. **Environment variables**: Set `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, and `AZURE_TENANT_ID`
2. **Managed Identity**: When running on Azure resources (VM, App Service, etc.)
3. **Azure CLI**: When `az login` has been run locally
4. **Azure PowerShell**: When `Connect-AzAccount` has been run

Make sure your authentication method has the necessary permissions to read public IP addresses in the specified subscription (typically requires `Network Contributor` or `Reader` role).

## Development

```bash
# Run tests
make test

# Build the binary
make build

# Clean build artifacts
make clean
```
