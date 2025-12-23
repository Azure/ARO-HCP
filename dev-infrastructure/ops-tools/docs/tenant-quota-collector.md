# Tenant Quota Collector

The Tenant Quota Collector is a workload that monitors Azure tenant quota usage and exposes metrics for alerting and visualization.

## Overview

| Component | Description |
|-----------|-------------|
| **Purpose** | Monitor Azure tenant quota usage across multiple tenants |
| **Cluster** | `opstool` AKS cluster |
| **Metrics** | Exposed via Prometheus, sent to Azure Monitor Workspace |
| **Alerting** | Azure Monitor Prometheus rules with email notifications |

## Collection Intervals

| Component | Interval | Purpose |
|-----------|----------|---------|
| Collector → Azure API | 15 min | Fetches quota data from Microsoft Graph API |
| Prometheus → Collector | 15 min | Scrapes metrics endpoint |
| Azure Monitor → Evaluate | 1 min | Evaluates alert rules |

## Metrics

The collector exposes the following Prometheus metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `tenant_quota_total` | Gauge | Total tenant quota limit |
| `tenant_quota_used` | Gauge | Current quota usage |
| `tenant_quota_usage_percentage` | Gauge | Usage as percentage (0-100) |
| `tenant_remaining_capacity` | Gauge | Remaining quota capacity |

All metrics include labels:
- `tenant_id`
- `tenant_name`

## Alerts

Azure Monitor evaluates these alerts against the Azure Monitor Workspace:

| Alert | Threshold | Duration | Severity | Action |
|-------|-----------|----------|----------|--------|
| `TenantQuotaCritical` | ≥95% | 5 min | 2 (Critical) | Email |
| `TenantQuotaWarning` | ≥90% | 10 min | 3 (Warning) | Email |
| `TenantQuotaInfo` | ≥80% | 15 min | 4 (Info) | Email |
| `TenantQuotaMetricsStale` | No data | 3 days | 2 (Critical) | Email |

The stale metrics alert fires if no data is received for 3 days, indicating the collector may be down or the service principal token expired.

**Production notification recipient:** aro-hcp-service-lifecycle-team@redhat.com

## Configuration

### Config File

The collector is configured via `config/config-opstool.yaml`:

```yaml
opstool:
  tenantQuota:
    image:
      registry: "arohcpsvcdev.azurecr.io"
      repository: "tenant-quota-collector"
      tag: "latest"
  alerting:
    email: "aro-hcp-service-lifecycle-team@redhat.com"
    enabled: true
```

### Adding Tenants

Tenants are configured in the Helm values file. To add a new tenant:

1. Create a Service Principal in the target tenant with Reader access
2. Add the client secret to Key Vault:
   ```bash
   az keyvault secret set \
     --vault-name opstool-kv-usw3 \
     --name my-tenant-client-secret \
     --value "<client-secret>"
   ```
3. Update `dev-infrastructure/ops-tools/tenant-quota/deploy/values.yaml`:
   ```yaml
   tenants:
   - tenantName: "MyTenant"
     tenantId: "<tenant-id>"
     servicePrincipalClientId: "<client-id>"
     keyVaultSecretName: "my-tenant-client-secret"
   ```
4. Redeploy

## Deployment

### Prerequisites

- Access to the `opstool` environment
- Azure CLI logged in
- templatize tool built

### Deploy Commands

```bash
# Deploy infrastructure (if needed)
./tooling/templatize/templatize pipeline run \
  --config-file="$(pwd)/config/config-opstool.yaml" \
  --topology-file="$(pwd)/topology-opstool.yaml" \
  --dev-settings-file="$(pwd)/tooling/templatize/settings.yaml" \
  --dev-environment opstool \
  --service-group Microsoft.Azure.ARO.HCP.Opstool.Infra

# Deploy tenant-quota collector and alerting
./tooling/templatize/templatize pipeline run \
  --config-file="$(pwd)/config/config-opstool.yaml" \
  --topology-file="$(pwd)/topology-opstool.yaml" \
  --dev-settings-file="$(pwd)/tooling/templatize/settings.yaml" \
  --dev-environment opstool \
  --service-group Microsoft.Azure.ARO.HCP.Opstool.TenantQuota
```

## Verification

### Check Pod Status

```bash
kubectl get pods -n tenant-quota
```

### View Logs

```bash
kubectl logs -n tenant-quota deployment/tenant-quota-collector
```

### Check Metrics Endpoint

```bash
kubectl port-forward -n tenant-quota svc/tenant-quota-collector 8080:8080
curl http://localhost:8080/metrics | grep tenant_quota
```

### Verify Prometheus Scraping

```bash
kubectl port-forward -n prometheus svc/prometheus-operated 9090:9090
curl "http://localhost:9090/api/v1/query?query=tenant_quota_total" | jq '.data.result'
```

### View in Azure Portal

1. **Azure Monitor Workspace**: [opstool-monitor-usw3](https://portal.azure.com/#@redhat.com/resource/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/opstool-westus3/providers/Microsoft.Monitor/accounts/opstool-monitor-usw3/overview)

2. **Prometheus Rule Group**: [tenant-quota-alerts](https://portal.azure.com/#@redhat.com/resource/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/opstool-westus3/providers/Microsoft.AlertsManagement/prometheusRuleGroups/tenant-quota-alerts/overview)

3. **Shared Action Group**: [opstool-email-alerts](https://portal.azure.com/#@redhat.com/resource/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/opstool-westus3/providers/microsoft.insights/actionGroups/opstool-email-alerts/overview)

## Azure Resources

| Resource | Type | Deployed By | Resource Group |
|----------|------|-------------|----------------|
| `opstool-usw3` | AKS Cluster | Infra | `opstool-westus3` |
| `opstool-monitor-usw3` | Azure Monitor Workspace | Infra | `opstool-westus3` |
| `opstool-email-alerts` | Shared Action Group | Infra | `opstool-westus3` |
| `tenant-quota-alerts` | Prometheus Rule Group | TenantQuota | `opstool-westus3` |
| `opstool-kv-usw3` | Key Vault | Infra | `opstool-westus3` |
| `tenant-quota` | User-Assigned Managed Identity | Infra | `opstool-westus3` |

## Troubleshooting

### Pod Not Starting

1. Check events: `kubectl describe pod -n tenant-quota <pod-name>`
2. Common issues:
   - **ImagePullBackOff**: ACR not attached or image doesn't exist
   - **ContainerCreating**: Key Vault secrets issue (check tenantId in SecretProviderClass)

### No Metrics in Prometheus

1. Check ServiceMonitor exists: `kubectl get servicemonitor -n tenant-quota`
2. Verify ServiceMonitor has `release: arohcp-monitor` label
3. Check Prometheus targets: port-forward to 9090 and check `/targets`

### Alerts Not Firing

1. Verify alert rules in Azure Portal
2. Check metrics exist in Azure Monitor Workspace
3. Test action group: 
   ```bash
   az monitor action-group test-notifications create \
     --action-group tenant-quota-email-alerts \
     --resource-group opstool-westus3 \
     -a email test your-email@redhat.com usecommonalertsChema \
     --alert-type budget
   ```

### Service Principal Token Expired

If you receive a `TenantQuotaMetricsStale` alert, the service principal token may have expired.

The tenant/SP info is hardcoded in the TENANTS array within the scrip and matched to details in values.yaml. Have been written to be added to an automated system for future enhancements.

**Check current secret expiration:**

```bash
cd dev-infrastructure/ops-tools/tenant-quota
./scripts/renew-sp-secret.sh --list
```

**Renew the token using the script:**

```bash
cd dev-infrastructure/ops-tools/tenant-quota

# IMPORTANT: Login to the correct Azure AD tenant first!
# The script will show you which tenant ID to use.
az login --tenant <azure-ad-tenant-id>

# Renew with 1-year expiry (default)
./scripts/renew-sp-secret.sh --tenant RedHat0

# Renew with 2-year expiry and restart pod (may depend on policy)
./scripts/renew-sp-secret.sh --tenant RedHat0 --expiry 2 --restart
```

**Multi-tenant note:** If you have SPs in different Azure AD tenants, you must login to each tenant separately to renew its SP. The script will prompt you and show the correct tenant ID.

**Manual renewal (if script not available):**

1. Go to Azure AD → App registrations → Find the service principal
2. Navigate to Certificates & secrets
3. Create a new client secret (note the expiry date)
4. Update the Key Vault secret:
   ```bash
   az keyvault secret set \
     --vault-name opstool-kv-usw3 \
     --name custom-metrics-collector-redhat0-client-secret \
     --value "<new-client-secret>"
   ```
5. Restart the collector pod to pick up the new secret:
   ```bash
   kubectl rollout restart deployment/tenant-quota-collector -n tenant-quota
   ```

## Files

| File | Purpose |
|------|---------|
| `dev-infrastructure/ops-tools/tenant-quota/main.go` | Go application entry point |
| `dev-infrastructure/ops-tools/tenant-quota/pkg/` | Go packages (config, tenantquota) |
| `dev-infrastructure/ops-tools/tenant-quota/pipeline.yaml` | TenantQuota deployment pipeline |
| `dev-infrastructure/ops-tools/tenant-quota/alerting.bicep` | App-specific Prometheus alert rules |
| `dev-infrastructure/ops-tools/tenant-quota/deploy/` | Helm chart (values.yaml has tenant config) |
| `dev-infrastructure/ops-tools/tenant-quota/scripts/renew-sp-secret.sh` | Script to renew SP client secrets |
| `dev-infrastructure/templates/opstool-alerting.bicep` | Shared Action Group (Infra pipeline) |
| `config/config-opstool.yaml` | Environment configuration |
