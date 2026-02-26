// Prometheus alert rules for tenant-quota in the opstool environment
// Uses the shared Action Group from the Infra pipeline

@description('Azure Monitor Workspace resource ID')
param azureMonitorWorkspaceId string

@description('Shared Action Group resource ID from Infra pipeline')
param sharedActionGroupId string

@description('Enable or disable alerting')
param alertingEnabled bool = true

// Prometheus Rule Group for tenant-quota alerts
resource tenantQuotaAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'tenant-quota-alerts'
  location: resourceGroup().location
  properties: {
    enabled: alertingEnabled
    interval: 'PT1M'
    scopes: [
      azureMonitorWorkspaceId
    ]
    rules: [
      {
        alert: 'TenantQuotaCritical'
        enabled: true
        expression: 'tenant_quota_usage_percentage >= 95'
        for: 'PT5M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Tenant quota usage is critical'
          description: 'Tenant {{ $labels.tenant_name }} is at {{ $value }}% capacity'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
      {
        alert: 'TenantQuotaWarning'
        enabled: true
        expression: 'tenant_quota_usage_percentage >= 90 and tenant_quota_usage_percentage < 95'
        for: 'PT10M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'Tenant quota usage is high'
          description: 'Tenant {{ $labels.tenant_name }} is at {{ $value }}% capacity'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
      {
        alert: 'TenantQuotaInfo'
        enabled: true
        expression: 'tenant_quota_usage_percentage >= 80 and tenant_quota_usage_percentage < 90'
        for: 'PT15M'
        severity: 4
        labels: {
          severity: 'info'
        }
        annotations: {
          summary: 'Tenant quota usage is elevated'
          description: 'Tenant {{ $labels.tenant_name }} is at {{ $value }}% capacity'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
      {
        alert: 'TenantQuotaMetricsStale'
        enabled: true
        expression: 'absent(tenant_quota_usage_percentage)'
        for: 'P3D'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Tenant quota metrics are stale'
          description: 'No tenant_quota_usage_percentage metrics received for 3 days. Possible causes: (1) Collector pod is down - check: kubectl get pods -n tenant-quota, (2) Service principal token expired - run: cd dev-infrastructure/ops-tools/tenant-quota && ./scripts/renew-sp-secret.sh --list to check expiry, then ./scripts/renew-sp-secret.sh --tenant <name> --restart to renew, (3) Prometheus not scraping - check ServiceMonitor in tenant-quota namespace. See dev-infrastructure/ops-tools/docs/tenant-quota-collector.md for full troubleshooting.'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT1H'
        }
      }
    ]
  }
}

output alertRuleGroupId string = tenantQuotaAlerts.id
