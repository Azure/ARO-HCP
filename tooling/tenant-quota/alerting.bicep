// Prometheus alert rules for tenant-quota in the opstool environment
// Uses the shared Action Group from the Infra pipeline

@description('Azure Monitor Workspace resource ID')
param azureMonitorWorkspaceId string

@description('Shared Action Group resource ID from Infra pipeline')
param sharedActionGroupId string

@description('Enable or disable alerting')
param alertingEnabled bool = true

// Usage/limit ratio excluding Network Watchers
var azureQuotaUsageRatioFiltered = 'azure_quota_usage{localized_name!~"(?i)^network watchers$"} / azure_quota_limit{localized_name!~"(?i)^network watchers$"}'

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
        alert: 'TenantQuotaCollectorUp'
        enabled: true
        expression: 'absent(up{job="tenant-quota-collector",namespace="tenant-quota"} == 1)'
        for: 'PT15M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'Tenant quota collector is unreachable'
          description: 'tenant-quota-collector has not been reachable for 15 minutes. Check the pod status, service endpoints, and Prometheus scrape target health in the tenant-quota namespace.'
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
        for: 'PT6H'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Tenant quota metrics are stale'
          description: 'No tenant_quota_usage_percentage metrics received for 6 hours. Possible causes: (1) Collector pod is down - check: kubectl get pods -n tenant-quota, (2) Service principal token expired - run: cd tooling/tenant-quota && ./scripts/renew-sp-secret.sh --list to check expiry, then ./scripts/renew-sp-secret.sh --tenant <name> to renew, (3) Prometheus not scraping - check ServiceMonitor in tenant-quota namespace. See tooling/tenant-quota/README.md for full troubleshooting.'
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

resource subscriptionQuotaAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'subscription-quota-alerts'
  location: resourceGroup().location
  properties: {
    enabled: alertingEnabled
    interval: 'PT1M'
    scopes: [
      azureMonitorWorkspaceId
    ]
    rules: [
      {
        alert: 'AzureQuotaCritical'
        enabled: true
        expression: '${azureQuotaUsageRatioFiltered} > 0.95'
        for: 'PT5M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Azure quota critical: {{ $labels.source }}/{{ $labels.quota_name }}'
          description: '{{ $labels.quota_name }} at {{ $value | humanizePercentage }} in {{ $labels.subscription_name }}/{{ $labels.region }}'
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
        alert: 'AzureQuotaWarning'
        enabled: true
        expression: '${azureQuotaUsageRatioFiltered} > 0.80 and ${azureQuotaUsageRatioFiltered} <= 0.95'
        for: 'PT10M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'Azure quota warning: {{ $labels.source }}/{{ $labels.quota_name }}'
          description: '{{ $labels.quota_name }} at {{ $value | humanizePercentage }} in {{ $labels.subscription_name }}/{{ $labels.region }}'
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
        alert: 'AzureQuotaMetricsStale'
        enabled: true
        expression: 'absent(azure_quota_usage)'
        for: 'PT30M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Subscription quota usage metrics are stale'
          description: 'No azure_quota_usage metrics received for 30 minutes. Check the tenant-quota-collector pod status, Prometheus scrape target health, and service principal credentials.'
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
      {
        alert: 'AzureQuotaLimitMetricsStale'
        enabled: true
        expression: 'absent(azure_quota_limit)'
        for: 'PT30M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Subscription quota limit metrics are stale'
          description: 'No azure_quota_limit metrics received for 30 minutes. Check the tenant-quota-collector pod status, Prometheus scrape target health, and service principal credentials.'
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

resource e2eExpiredRGAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'e2e-expired-resource-group-alerts'
  location: resourceGroup().location
  properties: {
    enabled: alertingEnabled
    interval: 'PT1M'
    scopes: [
      azureMonitorWorkspaceId
    ]
    rules: [
      {
        // TODO: tighten threshold to > 0 once cleanup-sweeper reliably clears expired RGs
        alert: 'E2EExpiredResourceGroupsInfo'
        enabled: true
        expression: 'e2e_resource_group_expired > 10'
        for: 'PT2H'
        severity: 4
        labels: {
          severity: 'info'
        }
        annotations: {
          summary: 'Expired E2E resource groups detected'
          description: '{{ $value }} E2E resource groups past their deleteAfter TTL in {{ $labels.subscription_name }}/{{ $labels.region }}. Check the cleanup-sweeper and periodic cleanup job.'
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
        // TODO: tighten to > 5 / sev 3 once cleanup-sweeper baseline improves
        alert: 'E2EExpiredResourceGroupsEscalation'
        enabled: true
        expression: 'e2e_resource_group_expired > 25'
        for: 'PT2H'
        severity: 4
        labels: {
          severity: 'info'
        }
        annotations: {
          summary: 'Many expired E2E resource groups detected'
          description: '{{ $value }} E2E resource groups past their deleteAfter TTL in {{ $labels.subscription_name }}/{{ $labels.region }}. Resource cleanup is likely broken.'
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
        // TODO: tighten to > 86400 (1 day) / sev 3 once cleanup-sweeper baseline improves
        alert: 'E2EExpiredResourceGroupStale'
        enabled: true
        expression: 'e2e_resource_group_expired_max_age_seconds > 604800'
        for: 'PT15M'
        severity: 4
        labels: {
          severity: 'info'
        }
        annotations: {
          summary: 'E2E resource group expired for over 7 days'
          description: 'Oldest expired E2E resource group in {{ $labels.subscription_name }}/{{ $labels.region }} has been past its TTL for {{ $value | humanizeDuration }}. Manual cleanup may be required.'
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
        alert: 'E2EResourceGroupMetricsStale'
        enabled: true
        expression: 'absent(e2e_resource_group_active)'
        for: 'PT30M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'E2E resource group metrics are stale'
          description: 'No e2e_resource_group_active metrics received for 30 minutes. The resource group collector may have stopped. Check the tenant-quota-collector pod status and Prometheus scrape target health.'
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
    ]
  }
}

output alertRuleGroupId string = tenantQuotaAlerts.id
output subscriptionAlertRuleGroupId string = subscriptionQuotaAlerts.id
output e2eExpiredRGAlertRuleGroupId string = e2eExpiredRGAlerts.id
