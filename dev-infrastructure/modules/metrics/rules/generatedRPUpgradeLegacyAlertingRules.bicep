#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

@description('Whether alert rules in this generated module are evaluated.')
param alertRulesEnabled bool = true

resource rpUserJourneyClusterUpgradeMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-user-journey-cluster-upgrade-monitor-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyClusterUpgradeStuckInDesired'
        enabled: alertRulesEnabled
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-upgrade-reach-partial'
        }
        annotations: {
          correlationId: 'userJourneyClusterUpgradeStuckInDesired/{{ $labels.cluster }}{{ $labels.resource_id }}/{{ $labels.version }}'
          description: '''Cluster version change (upgrade or rollback) to {{ $labels.version }} has been in desired for over 20 minutes without reaching partial. With the alert pending for 5 minutes, paging starts after ~25 minutes. Partial is when the target version becomes active (recognized as active) on the HCP cluster; the version change is in progress but not yet complete. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''Cluster version change (upgrade or rollback) to {{ $labels.version }} has been in desired for over 20 minutes without reaching partial. With the alert pending for 5 minutes, paging starts after ~25 minutes. Partial is when the target version becomes active (recognized as active) on the HCP cluster; the version change is in progress but not yet complete. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-upgrade'
          summary: '{{ $labels.cluster }}: Cluster version change on {{ $labels.resource_id }} to version {{ $labels.version }} not progressing'
          title: '{{ $labels.cluster }}: Cluster version change on {{ $labels.resource_id }} to version {{ $labels.version }} not progressing'
        }
        expression: 'hosted_control_plane_upgrade:duration_in_desired:seconds > 1200'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'userJourneyClusterUpgradeStuckInProgress'
        enabled: alertRulesEnabled
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-upgrade-complete'
        }
        annotations: {
          correlationId: 'userJourneyClusterUpgradeStuckInProgress/{{ $labels.cluster }}{{ $labels.resource_id }}/{{ $labels.version }}'
          description: '''Cluster version change (upgrade or rollback) to {{ $labels.version }} has been in progress for over 30 minutes without reaching completed. With the alert pending for 5 minutes, paging starts after ~35 minutes. Completed is when the target version change has finished and is reported as complete on the HCP cluster. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''Cluster version change (upgrade or rollback) to {{ $labels.version }} has been in progress for over 30 minutes without reaching completed. With the alert pending for 5 minutes, paging starts after ~35 minutes. Completed is when the target version change has finished and is reported as complete on the HCP cluster. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-upgrade'
          summary: '{{ $labels.cluster }}: Cluster version change on {{ $labels.resource_id }} to version {{ $labels.version }} stuck'
          title: '{{ $labels.cluster }}: Cluster version change on {{ $labels.resource_id }} to version {{ $labels.version }} stuck'
        }
        expression: 'hosted_control_plane_upgrade:duration_in_progress:seconds > 1800'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
