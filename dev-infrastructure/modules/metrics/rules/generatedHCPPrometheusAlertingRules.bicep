#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource mgmtCapacityRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'mgmt-capacity-rules'
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
        alert: 'MgmtClusterHCPCapacityWarning'
        enabled: true
        labels: {
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityWarning/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          info: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster HCP capacity is approaching limit (60% threshold).'
          title: 'Management cluster HCP capacity is approaching limit (60% threshold). cluster:{{ $labels.cluster }}'
        }
        expression: '(count by (cluster) (kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) / 60) > 0.6'
        for: 'PT15M'
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
        alert: 'MgmtClusterNodeSwiftNICCapacityZero'
        enabled: true
        labels: {
          severity: 'critical'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterNodeSwiftNICCapacityZero/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity. No HCPs can be scheduled on this node until NIC capacity is restored.'
          info: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity. No HCPs can be scheduled on this node until NIC capacity is restored.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://portal.microsofticm.com/imp/v5/incidents/details/802529667'
          summary: 'Management cluster node has zero SWIFT NIC capacity.'
          title: 'Management cluster node has zero SWIFT NIC capacity. node:{{ $labels.node }} cluster:{{ $labels.cluster }}'
        }
        expression: 'kube_node_status_capacity{node=~"user.*",resource="aro_openshift_io_swift_nic"} == 0'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityCritical/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          info: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster HCP capacity is critically high (85% threshold).'
          title: 'Management cluster HCP capacity is critically high (85% threshold). cluster:{{ $labels.cluster }}'
        }
        expression: '(count by (cluster) (kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) / 60) > 0.85'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
