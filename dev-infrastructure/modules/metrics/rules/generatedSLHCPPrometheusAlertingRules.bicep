#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource hcpClusterOperatorsRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-cluster-operators-rules'
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
        alert: 'HCPClusterOperatorUnavailable'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'HCPClusterOperatorUnavailable/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Available=false for more than 30 minutes. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. An unavailable operator means the component it manages is down, not merely degraded.
'''
          info: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Available=false for more than 30 minutes. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. An unavailable operator means the component it manages is down, not merely degraded.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hcp-cluster-operators.md'
          summary: 'HCP cluster operator unavailable on {{ $labels.namespace }}'
          title: 'HCP cluster operator unavailable on {{ $labels.namespace }} cluster:{{ $labels.cluster }}'
        }
        expression: 'count by (cluster, namespace) (cluster_operator_conditions{condition="available",name!~"version|console"} == 0) and on (cluster, namespace) (sum by (cluster, namespace) (node_collector_zone_size) > 0)'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'HCPClusterOperatorDegraded'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'HCPClusterOperatorDegraded/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Degraded=true for more than 2 hours. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. A degraded operator is reporting reduced quality of service.
'''
          info: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Degraded=true for more than 2 hours. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. A degraded operator is reporting reduced quality of service.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hcp-cluster-operators.md'
          summary: 'HCP cluster operator degraded on {{ $labels.namespace }}'
          title: 'HCP cluster operator degraded on {{ $labels.namespace }} cluster:{{ $labels.cluster }}'
        }
        expression: 'count by (cluster, namespace) (cluster_operator_conditions{condition="degraded",name!~"version|console"} == 1) and on (cluster, namespace) (sum by (cluster, namespace) (node_collector_zone_size) > 0)'
        for: 'PT2H'
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
        alert: 'HCPClusterVersionFailing'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'HCPClusterVersionFailing/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''The version operator (ClusterVersion) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) has been Failing for more than 1 hour while NO other cluster operator is unavailable or degraded. This points at a version-operator-specific failure -- payload image retrieval, release signature verification, an upgrade precondition, or a cluster-scoped manifest apply -- rather than a problem any individual component operator reports.
'''
          info: '''The version operator (ClusterVersion) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) has been Failing for more than 1 hour while NO other cluster operator is unavailable or degraded. This points at a version-operator-specific failure -- payload image retrieval, release signature verification, an upgrade precondition, or a cluster-scoped manifest apply -- rather than a problem any individual component operator reports.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hcp-cluster-operators.md'
          summary: 'HCP version operator failing on {{ $labels.namespace }}'
          title: 'HCP version operator failing on {{ $labels.namespace }} cluster:{{ $labels.cluster }}'
        }
        expression: 'count by (cluster, namespace) (cluster_operator_conditions{condition="failing",name="version"} == 1) unless on (cluster, namespace) count by (cluster, namespace) ((cluster_operator_conditions{condition="available",name!="version"} == 0) or (cluster_operator_conditions{condition="degraded",name!="version"} == 1))'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
