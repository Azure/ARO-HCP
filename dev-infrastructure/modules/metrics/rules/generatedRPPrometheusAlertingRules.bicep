#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource arohcpAccessClusterSloErrorAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_access_cluster_slo_error_alerts'
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
        alert: 'userJourneyAccessClusterErrors1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: 'info'
          short_window: '5m'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of credential operations (requestcredential/revokecredentials) are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          info: 'More than 72% of credential operations (requestcredential/revokecredentials) are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation error rate critically high (>72%)'
          title: '{{ $labels.cluster }}: Credential operation error rate critically high (>72%)'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.72'
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
        alert: 'userJourneyAccessClusterErrors6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'info'
          short_window: '30m'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of credential operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of credential operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation error rate elevated (>30%) for 30+ minutes'
          title: '{{ $labels.cluster }}: Credential operation error rate elevated (>30%) for 30+ minutes'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.30'
        for: 'PT30M'
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
        alert: 'userJourneyAccessClusterErrors3d'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'info'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of credential operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of credential operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation error rate exceeds SLO target (>5%) for 6+ hours'
          title: '{{ $labels.cluster }}: Credential operation error rate exceeds SLO target (>5%) for 6+ hours'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.05'
        for: 'PT6H'
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
        alert: 'userJourneyAccessClusterErrorsDegradation'
        enabled: true
        labels: {
          severity: 'info'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrorsDegradation/{{ $labels.cluster }}'
          description: 'The credential operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The credential operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation failure rate exceeds 15% for 30 minutes'
          title: '{{ $labels.cluster }}: Credential operation failure rate exceeds 15% for 30 minutes'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.15'
        for: 'PT30M'
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
        alert: 'userJourneyAccessClusterStuckOperation'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterStuckOperation/{{ $labels.cluster }}'
          description: 'Credential operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          info: 'Credential operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation stuck in {{ $labels.phase }} for over 1 hour'
          title: '{{ $labels.cluster }}: Credential operation stuck in {{ $labels.phase }} for over 1 hour'
        }
        expression: '( (time() - backend_resource_operation_start_time_seconds{ resource_type="microsoft.redhatopenshift/hcpopenshiftclusters", operation_type=~"requestcredential|revokecredentials" }) and backend_resource_operation_phase_info{ resource_type="microsoft.redhatopenshift/hcpopenshiftclusters", operation_type=~"requestcredential|revokecredentials", phase=~"accepted|provisioning|deleting" } == 1 ) > 3600'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpAccessClusterSaturationAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_access_cluster_saturation_alerts'
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
        alert: 'userJourneyAccessClusterSaturationQueueDepth'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterSaturationQueueDepth/{{ $labels.cluster }}'
          description: 'Credential controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Credential controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} depth is high'
          title: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} depth is high'
        }
        expression: 'max by (name, cluster) ( max without(prometheus_replica) ( workqueue_depth{namespace="aro-hcp", name=~".*(RequestCredential|RevokeCredentials).*"} ) ) > 10'
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
        alert: 'userJourneyAccessClusterSaturationRetryHotLoop'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterSaturationRetryHotLoop/{{ $labels.cluster }}'
          description: 'Credential controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Credential controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} retry hot loop'
          title: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '( sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_retries_total{namespace="aro-hcp", name=~".*(RequestCredential|RevokeCredentials).*"}[10m]) ) ) / sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_adds_total{namespace="aro-hcp", name=~".*(RequestCredential|RevokeCredentials).*"}[10m]) ) ) ) > 0.5'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

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
          long_window: '1h'
          severity: 'info'
          short_window: '5m'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          info: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          runbook_url: 'aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation error rate critically high (>72%)'
          title: '{{ $labels.cluster }}: Node Pool operation error rate critically high (>72%)'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.72'
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
        alert: 'UJNodePoolErrors6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'info'
          short_window: '30m'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation error rate elevated (>30%) for 30+ minutes'
          title: '{{ $labels.cluster }}: Node Pool operation error rate elevated (>30%) for 30+ minutes'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.3'
        for: 'PT30M'
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
        alert: 'UJNodePoolErrors3d'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'info'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
          title: '{{ $labels.cluster }}: Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.05'
        for: 'PT6H'
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
        alert: 'UJNodePoolErrorsDegradation'
        enabled: true
        labels: {
          severity: 'info'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrorsDegradation/{{ $labels.cluster }}'
          description: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation failure rate exceeds 15% for 30 minutes'
          title: '{{ $labels.cluster }}: Node Pool operation failure rate exceeds 15% for 30 minutes'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.15'
        for: 'PT30M'
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
        alert: 'UJNodePoolStuckOperation'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'UJNodePoolStuckOperation/{{ $labels.cluster }}'
          description: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 2 hours. Stuck operations are invisible to success/failure SLIs and require investigation.'
          info: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 2 hours. Stuck operations are invisible to success/failure SLIs and require investigation.'
          runbook_url: 'aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation stuck in {{ $labels.phase }} for over 2 hours'
          title: '{{ $labels.cluster }}: Node Pool operation stuck in {{ $labels.phase }} for over 2 hours'
        }
        expression: '((time() - backend_resource_operation_start_time_seconds{resource_type=~".*nodepools"}) and backend_resource_operation_phase_info{phase=~"updating|deleting",resource_type=~".*nodepools"} == 1) > 7200'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpNodepoolSaturationAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_nodepool_saturation_alerts'
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
        alert: 'UJNodePoolSaturationQueueDepth'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'UJNodePoolSaturationQueueDepth/{{ $labels.cluster }}'
          description: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} depth is high'
          title: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} depth is high'
        }
        expression: 'max by (name, cluster) (max without (prometheus_replica) (workqueue_depth{name=~".*NodePool.*",namespace="aro-hcp"})) > 10'
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
        alert: 'UJNodePoolSaturationRetryHotLoop'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'UJNodePoolSaturationRetryHotLoop/{{ $labels.cluster }}'
          description: 'Node pool controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Node pool controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} retry hot loop'
          title: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '(sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_retries_total{name=~".*NodePool.*",namespace="aro-hcp"}[10m]))) / sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_adds_total{name=~".*NodePool.*",namespace="aro-hcp"}[10m])))) > 0.5'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
