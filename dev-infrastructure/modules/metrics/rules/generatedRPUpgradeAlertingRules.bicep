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

resource rpUserJourneyClusterUpgradeMonitorRulesV2 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-user-journey-cluster-upgrade-monitor-rules-v2'
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
        alert: 'userJourneyClusterUpgradeStuck'
        enabled: alertRulesEnabled
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-upgrade'
        }
        annotations: {
          correlationId: 'userJourneyClusterUpgradeStuck/{{ $labels.cluster }}{{ $labels.resource_id }}'
          description: '''The cluster version change (upgrade or rollback) has not converged. The target exceeded 20 minutes in desired or 30 minutes in progress, with a further 5-minute alert pending window. This cluster-level condition remains active across target and phase changes until three valid observations confirm that resolved desired equals the newest current version and is completed, or confirm deletion/uninstallation. Missing observations hold the monitor and pause confirmation. After a rule-evaluator outage, the latest recorded state is reconciled using these same completion and lifecycle checks. Inspect backend_cluster_version_info and backend_cluster_upgrade_target_info for version details.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''The cluster version change (upgrade or rollback) has not converged. The target exceeded 20 minutes in desired or 30 minutes in progress, with a further 5-minute alert pending window. This cluster-level condition remains active across target and phase changes until three valid observations confirm that resolved desired equals the newest current version and is completed, or confirm deletion/uninstallation. Missing observations hold the monitor and pause confirmation. After a rule-evaluator outage, the latest recorded state is reconciled using these same completion and lifecycle checks. Inspect backend_cluster_version_info and backend_cluster_upgrade_target_info for version details.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-upgrade'
          summary: '{{ $labels.cluster }}: Cluster upgrade on {{ $labels.resource_id }} is stuck'
          title: '{{ $labels.cluster }}: Cluster upgrade on {{ $labels.resource_id }} is stuck'
        }
        expression: 'hosted_control_plane_upgrade:stuck:bool == 1'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
