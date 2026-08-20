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

// E2E resource group alert thresholds — relaxed initially; see TODOs on each rule
var e2eExpiredCountInfoThreshold = 10 // TODO: tighten to 0 once cleanup-sweeper reliably clears expired RGs
var e2eExpiredCountEscalationThreshold = 25 // TODO: tighten to 5 once cleanup-sweeper baseline improves
var e2eMaxExpiredAgeSeconds = 604800 // 7 days; TODO: tighten to 86400 (1 day)

// Prow CI alert thresholds
var prowHighFrequencyMinRuns = 5
var prowScheduledMinRuns = 3
var prowHealthcheckMaxFailureRate = '0.40'
var prowE2EParallelMinSuccessfulRuns = 20
var prowE2EParallelP95MaxSeconds = 9000 // 2h30m
var prowCollectionMaxAgeSeconds = 900 // 15 minutes
var prowBatchMaxConsecutiveFailures = 4
var prowInvalidJobsMaxPercentage = '0.10'

var prowHighFrequencyRuns = 'sum by (job_name, job_type) (prow_ci_job_info{job_type=~"presubmit|batch"})'
var prowHighFrequencyFailures = 'sum by (job_name, job_type) (prow_ci_job_info{job_type=~"presubmit|batch",result=~"failure|error"})'
var prowScheduledRuns = 'sum by (job_name, job_type) (prow_ci_job_info{job_type=~"periodic|postsubmit"})'
var prowScheduledFailures = 'sum by (job_name, job_type) (prow_ci_job_info{job_type=~"periodic|postsubmit",result=~"failure|error"})'
var prowHealthcheckRuns = 'sum by (job_name) (prow_ci_job_info{job_name=~"periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-.*"})'
var prowHealthcheckFailures = 'sum by (job_name) (prow_ci_job_info{job_name=~"periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-.*",result=~"failure|error"})'

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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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
        alert: 'E2EExpiredResourceGroupsInfo'
        enabled: true
        expression: 'count by (subscription_id, subscription_name) (e2e_resource_group_expiry_timestamp < time()) > ${e2eExpiredCountInfoThreshold}'
        for: 'PT2H'
        severity: 4
        labels: {
          severity: 'info'
        }
        annotations: {
          summary: 'Expired E2E resource groups detected'
          description: '{{ $value }} E2E resource groups past their deleteAfter TTL in {{ $labels.subscription_name }}. Check the cleanup-sweeper and periodic cleanup job.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/cleanup.md'
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
        alert: 'E2EExpiredResourceGroupsEscalation'
        enabled: true
        expression: 'count by (subscription_id, subscription_name) (e2e_resource_group_expiry_timestamp < time()) > ${e2eExpiredCountEscalationThreshold}'
        for: 'PT2H'
        severity: 4
        labels: {
          severity: 'info'
        }
        annotations: {
          summary: 'Many expired E2E resource groups detected'
          description: '{{ $value }} E2E resource groups past their deleteAfter TTL in {{ $labels.subscription_name }}. Resource cleanup is likely broken.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/cleanup.md'
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
        alert: 'E2EExpiredResourceGroupStale'
        enabled: true
        expression: 'max by (subscription_id, subscription_name) (time() - (e2e_resource_group_expiry_timestamp < time())) > ${e2eMaxExpiredAgeSeconds}'
        for: 'PT15M'
        severity: 4
        labels: {
          severity: 'info'
        }
        annotations: {
          summary: 'E2E resource group expired for over {{ $value | humanizeDuration }}'
          description: 'Oldest expired E2E resource group in {{ $labels.subscription_name }} has been past its TTL for {{ $value | humanizeDuration }}. Manual cleanup may be required.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/cleanup.md'
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
        expression: 'absent(e2e_resource_group_expiry_timestamp)'
        for: 'PT30M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'E2E resource group metrics are stale'
          description: 'No e2e_resource_group_expiry_timestamp metrics received for 30 minutes. The resource group collector may have stopped. Check the tenant-quota-collector pod status and Prometheus scrape target health.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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

resource prowCIAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'prow-ci-alerts'
  location: resourceGroup().location
  properties: {
    enabled: alertingEnabled
    interval: 'PT1M'
    scopes: [
      azureMonitorWorkspaceId
    ]
    rules: [
      {
        alert: 'ProwCIHighFrequencyJobPermafailing'
        enabled: true
        expression: '(${prowHighFrequencyFailures} / ${prowHighFrequencyRuns}) == 1 and on (job_name, job_type) ${prowHighFrequencyRuns} >= ${prowHighFrequencyMinRuns}'
        for: 'PT15M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'Prow CI job is consistently failing'
          description: '{{ $labels.job_name }} ({{ $labels.job_type }}) has failed every run in the 24-hour window with at least ${prowHighFrequencyMinRuns} completed runs.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/operations.md'
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
        alert: 'ProwCIScheduledJobPermafailing'
        enabled: true
        expression: '(${prowScheduledFailures} / ${prowScheduledRuns}) == 1 and on (job_name, job_type) ${prowScheduledRuns} >= ${prowScheduledMinRuns}'
        for: 'PT30M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'Scheduled Prow CI job is consistently failing'
          description: '{{ $labels.job_name }} ({{ $labels.job_type }}) has failed every run in the 24-hour window with at least ${prowScheduledMinRuns} completed runs.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/operations.md'
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
        alert: 'ProwCIHealthcheckProvisionSuccessRateLow'
        enabled: true
        expression: 'label_replace((${prowHealthcheckFailures} / ${prowHealthcheckRuns}), "region", "$1", "job_name", ".*-provision-(.*)") > ${prowHealthcheckMaxFailureRate} and on (job_name) ${prowHealthcheckRuns} >= ${prowHighFrequencyMinRuns}'
        for: 'PT30M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'Regional Prow provision healthcheck success rate is below 60%'
          description: 'Provision healthchecks in {{ $labels.region }} have a {{ $value | humanizePercentage }} failure rate over the 24-hour window.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-region-failover.md'
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
        alert: 'ProwCIE2EParallelDurationP95High'
        enabled: true
        expression: 'histogram_quantile(0.95, sum by (job_name, le) (prow_ci_job_duration_window_runs{job_name="pull-ci-Azure-ARO-HCP-main-e2e-parallel",result="success"})) > ${prowE2EParallelP95MaxSeconds} and on (job_name) sum by (job_name) (prow_ci_job_duration_window_runs{job_name="pull-ci-Azure-ARO-HCP-main-e2e-parallel",result="success",le="+Inf"}) >= ${prowE2EParallelMinSuccessfulRuns}'
        for: 'PT30M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'Prow e2e-parallel P95 duration exceeds 2h30m'
          description: '{{ $labels.job_name }} successful-run P95 duration is {{ $value | humanizeDuration }} over the 24-hour window.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/operations.md'
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
        alert: 'ProwCICollectionStale'
        enabled: true
        expression: 'time() - prow_ci_collection_last_success_timestamp_seconds > ${prowCollectionMaxAgeSeconds}'
        for: 'PT15M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Prow CI metrics collection is stale'
          description: 'The last successful Prow collection was {{ $value | humanizeDuration }} ago. Check exporter logs and connectivity to prow.ci.openshift.org.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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
        alert: 'ProwCIInvalidJobsDetected'
        enabled: true
        expression: 'sum(increase(prow_ci_invalid_jobs_total[24h])) / (sum(prow_ci_cached_runs) + sum(increase(prow_ci_invalid_jobs_total[24h]))) > ${prowInvalidJobsMaxPercentage}'
        for: 'PT1M'
        severity: 3
        labels: {
          severity: 'warning'
        }
        annotations: {
          summary: 'High rate of malformed completed Prow CI jobs detected'
          description: 'Malformed completed Prow jobs exceeded 10% of all completed jobs in the last 24h.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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
        alert: 'ProwCINoCachedRuns'
        enabled: true
        expression: 'prow_ci_collection_success == 1 and prow_ci_cached_runs == 0'
        for: 'PT15M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Prow collection succeeds but exports no completed runs'
          description: 'The exporter can reach Prow but has no cached ARO-HCP runs. Check repository matching and the Prow response format.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/dev-ci-monitoring.md#exporter-health-checks'
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
        alert: 'ProwCIMergeQueueBlocked'
        enabled: true
        expression: 'prow_ci_job_consecutive_failures{job_type="batch"} >= ${prowBatchMaxConsecutiveFailures}'
        for: 'PT1M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'Prow merge queue job has failed ${prowBatchMaxConsecutiveFailures} consecutive times'
          description: '{{ $labels.job_name }} has {{ $value }} consecutive Tide batch failures. Inspect the merge queue and failing job before retesting.'
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/ci/operations.md'
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
output prowCIAlertRuleGroupId string = prowCIAlerts.id
