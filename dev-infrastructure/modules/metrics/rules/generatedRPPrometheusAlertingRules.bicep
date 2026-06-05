#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource arohcpNodepoolSloErrorAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_nodepool_slo_error_alerts'
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
        alert: 'UJNodePoolErrors1h5m'
        enabled: true
        labels: {
          severity: 'critical'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          info: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/node-pool-management-tsg.html'
          summary: 'Node Pool operation error rate critically high (>72%)'
          title: 'Node Pool operation error rate critically high (>72%)'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.72'
        for: 'PT5M'
        severity: 3
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
        alert: 'UJNodePoolErrors6h30m'
        enabled: true
        labels: {
          severity: 'critical'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/node-pool-management-tsg.html'
          summary: 'Node Pool operation error rate elevated (>30%) for 30+ minutes'
          title: 'Node Pool operation error rate elevated (>30%) for 30+ minutes'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.30'
        for: 'PT30M'
        severity: 3
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
        alert: 'UJNodePoolErrors3d'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/node-pool-management-tsg.html'
          summary: 'Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
          title: 'Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.05'
        for: 'PT6H'
        severity: 3
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
        alert: 'UJNodePoolErrorsDegradation'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrorsDegradation/{{ $labels.cluster }}'
          description: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/node-pool-management-tsg.html'
          summary: 'Node Pool operation failure rate exceeds 15% for 30 minutes'
          title: 'Node Pool operation failure rate exceeds 15% for 30 minutes'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.15'
        for: 'PT30M'
        severity: 3
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
        alert: 'UJNodePoolStuckOperation'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'UJNodePoolStuckOperation/{{ $labels.cluster }}'
          description: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          info: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/node-pool-management-tsg.html'
          summary: 'Node Pool operation stuck in {{ $labels.phase }} for over 1 hour'
          title: 'Node Pool operation stuck in {{ $labels.phase }} for over 1 hour'
        }
        expression: '( (time() - backend_resource_operation_start_time_seconds{resource_type=~".*nodepools"}) and backend_resource_operation_phase_info{resource_type=~".*nodepools", phase=~"updating|deleting"} == 1 ) > 3600'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpNodepoolComponentAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_nodepool_component_alerts'
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
        alert: 'CNodePoolQueueDepth'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'CNodePoolQueueDepth/{{ $labels.cluster }}'
          description: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/node-pool-management-tsg.html'
          summary: 'Node Pool controller workqueue {{ $labels.name }} depth is high'
          title: 'Node Pool controller workqueue {{ $labels.name }} depth is high'
        }
        expression: 'max by (name, cluster) ( max without(prometheus_replica) ( workqueue_depth{namespace="aro-hcp", name=~".*NodePool.*"} ) ) > 10'
        for: 'PT5M'
        severity: 3
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
        alert: 'CNodePoolRetryHotLoop'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'CNodePoolRetryHotLoop/{{ $labels.cluster }}'
          description: 'Node pool controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Node pool controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/node-pool-management-tsg.html'
          summary: 'Node Pool controller workqueue {{ $labels.name }} retry hot loop'
          title: 'Node Pool controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '( sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_retries_total{namespace="aro-hcp", name=~".*NodePool.*"}[10m]) ) ) / sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_adds_total{namespace="aro-hcp", name=~".*NodePool.*"}[10m]) ) ) ) > 0.5'
        for: 'PT10M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
