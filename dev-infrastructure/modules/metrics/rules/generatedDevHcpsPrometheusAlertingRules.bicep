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
          component: 'capacity'
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityWarning/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          info: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster {{ $labels.cluster }} HCP capacity approaching limit (60% threshold)'
          title: 'Management cluster {{ $labels.cluster }} HCP capacity approaching limit (60% threshold)'
        }
        expression: '(count by (cluster, region) (kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) / 60) > 0.6'
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
          component: 'capacity'
          severity: 'critical'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterNodeSwiftNICCapacityZero/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity. No HCPs can be scheduled on this node until NIC capacity is restored.'
          info: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity. No HCPs can be scheduled on this node until NIC capacity is restored.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://portal.microsofticm.com/imp/v5/incidents/details/802529667'
          summary: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity'
          title: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity'
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
          component: 'capacity'
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityCritical/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          info: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster {{ $labels.cluster }} HCP capacity critically high (85% threshold)'
          title: 'Management cluster {{ $labels.cluster }} HCP capacity critically high (85% threshold)'
        }
        expression: '(count by (cluster, region) (kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) / 60) > 0.85'
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
        alert: 'MgmtAgentCapacityReportingSyncFailing'
        enabled: true
        labels: {
          component: 'capacity'
          severity: 'warning'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtAgentCapacityReportingSyncFailing/{{ $labels.cluster }}'
          description: 'Capacity reporting on management cluster {{ $labels.cluster }} has been failing continuously for at least 5 minutes. The CapacityReport CR may contain stale data, which can affect fleet-level placement decisions. Check mgmt-agent logs for root cause.'
          info: 'Capacity reporting on management cluster {{ $labels.cluster }} has been failing continuously for at least 5 minutes. The CapacityReport CR may contain stale data, which can affect fleet-level placement decisions. Check mgmt-agent logs for root cause.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Capacity reporting sync failing on {{ $labels.cluster }}'
          title: 'Capacity reporting sync failing on {{ $labels.cluster }}'
        }
        expression: 'increase(capacity_reporting_sync_errors_total[5m]) >= 8'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'HCPClusterOperatorUnavailable/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Available=false for more than 30 minutes. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. An unavailable operator means the component it manages is down, not merely degraded.
'''
          info: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Available=false for more than 30 minutes. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. An unavailable operator means the component it manages is down, not merely degraded.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hcp-cluster-operators.md'
          summary: 'HCP cluster operator unavailable on {{ $labels.namespace }} ({{ $labels.cluster }})'
          title: 'HCP cluster operator unavailable on {{ $labels.namespace }} ({{ $labels.cluster }})'
        }
        expression: 'count by (cluster, namespace, region) (cluster_operator_conditions{condition="available",name!~"version|console"} == 0) and on (cluster, namespace) (sum by (cluster, namespace, region) (node_collector_zone_size) > 0)'
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
          component: 'kubernetes-infrastructure'
          severity: 'info'
        }
        annotations: {
          correlationId: 'HCPClusterOperatorDegraded/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Degraded=true for more than 2 hours. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. A degraded operator is reporting reduced quality of service.
'''
          info: '''{{ $value }} cluster operator(s) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) have been reporting Degraded=true for more than 2 hours. The version and console operators are excluded from this alert; the affected cluster has worker nodes present. A degraded operator is reporting reduced quality of service.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hcp-cluster-operators.md'
          summary: 'HCP cluster operator degraded on {{ $labels.namespace }} ({{ $labels.cluster }})'
          title: 'HCP cluster operator degraded on {{ $labels.namespace }} ({{ $labels.cluster }})'
        }
        expression: 'count by (cluster, namespace, region) (cluster_operator_conditions{condition="degraded",name!~"version|console"} == 1) and on (cluster, namespace) (sum by (cluster, namespace, region) (node_collector_zone_size) > 0)'
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
          component: 'kubernetes-infrastructure'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'HCPClusterVersionFailing/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''The version operator (ClusterVersion) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) has been Failing for more than 1 hour while NO other cluster operator is unavailable or degraded. This points at a version-operator-specific failure -- payload image retrieval, release signature verification, an upgrade precondition, or a cluster-scoped manifest apply -- rather than a problem any individual component operator reports.
'''
          info: '''The version operator (ClusterVersion) on hosted cluster {{ $labels.namespace }} (management cluster {{ $labels.cluster }}) has been Failing for more than 1 hour while NO other cluster operator is unavailable or degraded. This points at a version-operator-specific failure -- payload image retrieval, release signature verification, an upgrade precondition, or a cluster-scoped manifest apply -- rather than a problem any individual component operator reports.
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hcp-cluster-operators.md'
          summary: 'HCP version operator failing on {{ $labels.namespace }} ({{ $labels.cluster }})'
          title: 'HCP version operator failing on {{ $labels.namespace }} ({{ $labels.cluster }})'
        }
        expression: 'count by (cluster, namespace, region) (cluster_operator_conditions{condition="failing",name="version"} == 1) unless on (cluster, namespace) count by (cluster, namespace, region) ((cluster_operator_conditions{condition="available",name!="version"} == 0) or (cluster_operator_conditions{condition="degraded",name!="version"} == 1))'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource kubeApplier 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kube-applier'
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
        alert: 'KubeApplierDesiresMetricNotPresent'
        enabled: true
        labels: {
          component: 'kube-applier'
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubeApplierDesiresMetricNotPresent/{{ $labels.cluster }}'
          description: 'kube-applier on cluster {{ $labels.cluster }} lacks kube_applier_desires metric for 5 minutes.'
          info: 'kube-applier on cluster {{ $labels.cluster }} lacks kube_applier_desires metric for 5 minutes.'
          runbook_url: 'TBD'
          summary: 'kube-applier on cluster {{ $labels.cluster }} lacks kube_applier_desires metric'
          title: 'kube-applier on cluster {{ $labels.cluster }} lacks kube_applier_desires metric'
        }
        expression: 'kube_applier_health == 1 unless on (cluster, namespace) kube_applier_desires'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
