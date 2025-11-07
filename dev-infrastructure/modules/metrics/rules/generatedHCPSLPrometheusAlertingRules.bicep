#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource mgmtCapacityRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'mgmt-capacity.rules'
  location: resourceGroup().location
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
        alert: 'MgmtClusterHCPCapacityWarning'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityWarning/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster HCP capacity is approaching limit (60% threshold).'
          title: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
        }
        expression: '( count(kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) by (cluster) / 60 ) > 0.60'
        for: 'PT15M'
        severity: 4
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
        alert: 'MgmtClusterHCPCapacityCritical'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityCritical/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster HCP capacity is critically high (85% threshold).'
          title: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
        }
        expression: '( count(kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) by (cluster) / 60 ) > 0.85'
        for: 'PT5M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
