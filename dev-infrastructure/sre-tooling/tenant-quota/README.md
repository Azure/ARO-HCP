# Custom Metrics Collector

## Overview

The Custom Metrics Collector is a metrics collection framework that executes built-in Go collectors, exposing their output as Prometheus metrics. It runs collectors on configurable intervals, parses their output, and exposes metrics via HTTP for Prometheus scraping.

## Architecture

The Custom Metrics Collector follows an orchestrator pattern:

- **Collector (Orchestrator)**: The main `Collector` class (`pkg/collector/collector.go`) that:
  - Reads configuration from YAML
  - Registers Prometheus metrics
  - Schedules and executes individual collector functions
  - Parses output and updates Prometheus metrics
  - Exposes a single `/metrics` endpoint

- **Collector Functions** (individual gatherers): Built-in Go functions that:
  - Collect specific metrics from APIs (e.g., Graph API, Cost Management API)
  - Return data in `key=value` format
  - Can have their own Service Principal and Key Vault secrets
  - Are registered in `pkg/collector/register_builtins.go`
  - Examples: `tenant-quota`, `cost-monitor` (future)

**Terminology Note**: 
- `Collector` The orchestrator class
- `collectors` Individual metric collection functions
- `collector function` = A specific implementation (e.g., `tenant-quota`)

## Development Workflow

The Custom Metrics Collector can be built and tested locally and in personal DEV environments using a set of Makefile targets.

- **make run:** runs the Custom Metrics Collector binary locally
- **make deploy:** builds the Custom Metrics Collector container image, uploads it to the DEV service ACR and deploys it to a personal DEV cluster

### Local Run

Using the `make run` target, the Custom Metrics Collector binary can be run locally. The service requires a configuration file defining built-in Go collectors and their associated metrics.

**Creating a Test Configuration**

For local testing, create a YAML configuration file (e.g., `my-test-config.yaml`) with the following structure:

```yaml
collectors:
  - name: "tenant-quota"
    type: "builtin"
    id: "tenant-quota"
    interval: "30s"
    timeout: "30s"
    # Optional: Tenant-specific authentication
    # If not provided, uses default credential (e.g., Azure CLI, Managed Identity)
    tenants:
      - tenantId: "your-tenant-id"
        tenantName: "YourTenant"
        servicePrincipalClientId: "your-sp-client-id"
        keyVaultSecretName: "your-vault-secret-name"
    metrics:
      - name: "tenant_quota_usage_percentage"
        type: "gauge"
        help: "Tenant quota usage percentage"
        labels: ["tenant_id", "tenant_name"]
        source: "USAGE_PERCENTAGE"
      - name: "tenant_quota_total"
        type: "gauge"
        help: "Total tenant quota limit"
        labels: ["tenant_id", "tenant_name"]
        source: "QUOTA_TOTAL"
      - name: "tenant_quota_used"
        type: "gauge"
        help: "Used tenant quota from API"
        labels: ["tenant_id", "tenant_name"]
        source: "QUOTA_USED"
      - name: "tenant_remaining_capacity"
        type: "gauge"
        help: "Remaining tenant capacity"
        labels: ["tenant_id", "tenant_name"]
        source: "REMAINING_CAPACITY"
```

**Local Authentication**

When running locally (e.g., with `hack/test-local.sh`), the collector uses **Azure CLI authentication** via `DefaultAzureCredential`:

1. **No Key Vault required**: The test configuration does not include `tenants` or `auth` sections, so the collector uses `DefaultAzureCredential` which automatically detects your Azure CLI login.

2. **Azure CLI authentication**: The collector uses the token from your `az login` session to authenticate to Microsoft Graph API. Ensure you're logged in:
   ```bash
   az login
   ```

3. **Required permissions**: Your Azure CLI user account needs Microsoft Graph API permissions:
   - `Organization.Read.All` (required for `/organization` endpoint and `directorySizeQuota`)
   - Or `Directory.Read.All`

4. **Alternative authentication methods**: You can set environment variables if you wish:
   ```bash
   export AZURE_CLIENT_ID="your-client-id"
   export AZURE_CLIENT_SECRET="your-client-secret"
   export AZURE_TENANT_ID="your-tenant-id"
   ```
   `DefaultAzureCredential` will use these environment variables if they're set.

**Note**: The `tenants` configuration is optional for local runs. If omitted, the collector will use the default credential (Azure CLI, environment variables, or Managed Identity if running in Azure). Key Vault secrets are only used in production when the configuration includes `tenants` or `auth` sections with `keyVaultSecretName`.

**Running Locally**

By default, `make run` uses `./deploy/templates/configmap.yaml` (which contains Helm templating). 

**Option 1: Using the test script**

A script is available in `hack/test-local.sh` that builds the binary and runs it with a test configuration:

```bash
./hack/test-local.sh
```

**Option 2: Using make run**

To use your own test config with `make run`:

```bash
CONFIG_PATH=./my-test-config.yaml make run
```

**Option 3: Run directly**

```bash
CONFIG_PATH=./my-test-config.yaml PORT=8080 ./custom-metrics-collector
```

**Note**: Test configuration files like `my-test-config.yaml` are not committed to the repository. Create them locally as needed for testing.

### Personal DEV Environment deployment

The local code can also be deployed directly into a personal DEV environment by running `make deploy`. 

`make deploy` builds a custom developer image from the local code and uploads it to the DEV service ACR (`arohcpsvcdev`) into a developer specific repository.

## Deployment

The [pipeline.yaml](pipeline.yaml) file in this directory contains the pipeline definition for the Custom Metrics Collector. It is integrated into the [topology.yaml](../../topology.yaml) file and runs as part of the management cluster deployment.

## Configuration

Collectors and metrics are configured via a YAML configuration file mounted as a ConfigMap. The configuration defines:
- Collector names (must be registered built-in collectors)
- Execution intervals and timeouts
- Metric names, types, and labels
- Data source mappings from collector output to Prometheus metrics

### Collector Types

**Built-in Collector Functions (`type: builtin`)**
Built-in collector functions are Go-based implementations that are compiled into the binary. 

Each collector function:
- Implements a specific metric collection task (e.g., tenant quota, cost data)
- Can use its own Service Principal and Key Vault secret for authentication
- Returns data in `key=value` format
- Is registered in `pkg/collector/register_builtins.go`

Example: `tenant-quota` collector function that retrieves tenant quota information from Microsoft Graph API.

All collector functions are built-in Go implementations.

### Adding New Collector Functions

To add a new collector function (e.g., `cost-monitor`):

1. **Implement the collector function** in `pkg/collectors/{name}/{name}.go`
   - Function signature: `func CollectXxxFunc() func(context.Context) (string, error)`
   - Returns `key=value` formatted output
   - Can use per-collector authentication (Service Principal, Key Vault secret)

2. **Register it** in `pkg/collector/register_builtins.go`:
   ```go
   Register("cost-monitor", cost.CollectCostFunc())
   ```

3. **Add to config.yaml** (ConfigMap):
   ```yaml
   collectors:
     - name: cost-monitor
       type: builtin
       id: cost-monitor
       interval: 1h
       timeout: 30s
       auth: 
         servicePrincipalClientId: "cost-sp-id"
         keyVaultSecretName: "cost-client-secret"
       metrics:
         - name: azure_cost_total
           type: gauge
           help: "Total Azure cost"
           labels: [subscription_id]
           source: COST_TOTAL
   ```

4. **Create Service Principal** (manual, requires admin consent):
   - Create SP with appropriate API permissions
   - Get admin consent
   - Create client secret
   - Store in Key Vault

5. **Deploy**: The orchestrator automatically discovers and schedules the new collector function.

### Example Configuration

```yaml
collectors:
  - name: "tenant-quota"
    type: "builtin"
    id: "tenant-quota"
    interval: "5m"
    timeout: "30s"
    metrics:
      - name: "tenant_quota_usage_percentage"
        type: "gauge"
        help: "Tenant quota usage percentage"
        labels: ["tenant_id", "tenant_name"]
        source: "USAGE_PERCENTAGE"
```

## Monitoring

The service exposes metrics at `/metrics` and is automatically discovered by Prometheus via ServiceMonitor. Health checks are performed by the blackbox exporter via Probe resources, monitoring the `/healthz` endpoint every 30 seconds.

## Verification

To verify the Custom Metrics Collector is working correctly:

### Check ServiceMonitor

Verify that Prometheus is configured to scrape the service:

```bash
kubectl get servicemonitor custom-metrics-collector -n observability
```

### Check Probe

Verify that the blackbox exporter is probing the health endpoint:

```bash
kubectl get probe custom-metrics-collector-probe -n observability
```

### View Metrics

To view the metrics exposed by the service, port-forward to the service and curl the metrics endpoint:

```bash
kubectl port-forward -n observability svc/custom-metrics-collector 8080:8080
curl http://localhost:8080/metrics
```

Alternatively, view metrics directly via the service DNS name from within the cluster:

```bash
curl http://custom-metrics-collector.observability.svc.cluster.local:8080/metrics
```
