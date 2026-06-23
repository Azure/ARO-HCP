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
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
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
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
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
        expression: 'errors:backend_nodepool_operation:error_rate > 0.30'
        for: 'PT30M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
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
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
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
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
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
        expression: '( (time() - backend_resource_operation_start_time_seconds{resource_type=~".*nodepools"}) and backend_resource_operation_phase_info{resource_type=~".*nodepools", phase=~"updating|deleting"} == 1 ) > 7200'
        for: 'PT15M'
        severity: 4
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
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
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
        expression: 'max by (name, cluster) ( max without(prometheus_replica) ( workqueue_depth{namespace="aro-hcp", name=~".*NodePool.*"} ) ) > 10'
        for: 'PT5M'
        severity: 4
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
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
        expression: '( sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_retries_total{namespace="aro-hcp", name=~".*NodePool.*"}[10m]) ) ) / sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_adds_total{namespace="aro-hcp", name=~".*NodePool.*"}[10m]) ) ) ) > 0.5'
        for: 'PT10M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource lockboxAvailabilityRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'lockbox-availability-rules'
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
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
            }
          }
        ]
        alert: 'UJLockboxAuditDegraded'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'lockbox-availability'
        }
        annotations: {
          correlationId: 'UJLockboxAudit/{{ $labels.cluster }}'
          description: '''The Admin API failed to connect to the audit server and is running
with a no-op audit client. Breakglass session actions are NOT being
audited — this is a compliance violation.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''The Admin API failed to connect to the audit server and is running
with a no-op audit client. Breakglass session actions are NOT being
audited — this is a compliance violation.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-lockbox'
          summary: '[SVC] Lockbox audit log connection degraded on {{ $labels.cluster }}'
          title: '[SVC] Lockbox audit log connection degraded on {{ $labels.cluster }}'
        }
        expression: 'lockbox:audit_log_connection_degraded:max == 1'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource lockboxErrorsRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'lockbox-errors-rules'
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
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
            }
          }
        ]
        alert: 'UJLockboxAuditErrorRateHigh'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'lockbox-errors'
        }
        annotations: {
          correlationId: 'UJLockboxAuditErrors/{{ $labels.cluster }}'
          description: '''Audit log send error rate is {{ $value | humanizePercentage }} over the
last hour. Some breakglass session audit records may be lost.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''Audit log send error rate is {{ $value | humanizePercentage }} over the
last hour. Some breakglass session audit records may be lost.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-lockbox'
          summary: '[SVC] Lockbox audit log error rate >5% on {{ $labels.cluster }}'
          title: '[SVC] Lockbox audit log error rate >5% on {{ $labels.cluster }}'
        }
        expression: 'lockbox:audit_log_error_rate:ratio_1h > 0.05'
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
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
            }
          }
        ]
        alert: 'UJLockboxKasProxyErrors'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'lockbox-errors'
        }
        annotations: {
          correlationId: 'UJLockboxKasProxy/{{ $labels.cluster }}'
          description: '''KAS proxy error rate is {{ $value | humanizePercentage }}. Support
engineer\'s breakglass session may be unusable.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''KAS proxy error rate is {{ $value | humanizePercentage }}. Support
engineer\'s breakglass session may be unusable.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-lockbox'
          summary: '[SVC] Lockbox KAS proxy error rate >10% on {{ $labels.cluster }}'
          title: '[SVC] Lockbox KAS proxy error rate >10% on {{ $labels.cluster }}'
        }
        expression: '(lockbox:kas_proxy_errors_total:rate5m / lockbox:kas_proxy_requests_total:rate5m) > 0.1 and lockbox:kas_proxy_requests_total:rate5m > 0'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource lockboxLatencyRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'lockbox-latency-rules'
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
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
            }
          }
        ]
        alert: 'UJLockboxKasProxyLatencyHigh'
        enabled: true
        labels: {
          severity: 'warning'
          slo: 'lockbox-latency'
        }
        annotations: {
          correlationId: 'UJLockboxLatency/{{ $labels.cluster }}'
          description: '''KAS proxy p99 latency is {{ $value | humanizeDuration }}. Support
engineer may experience slow cluster access during breakglass session.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''KAS proxy p99 latency is {{ $value | humanizeDuration }}. Support
engineer may experience slow cluster access during breakglass session.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-lockbox'
          summary: '[SVC] Lockbox KAS proxy p99 latency >10s on {{ $labels.cluster }}'
          title: '[SVC] Lockbox KAS proxy p99 latency >10s on {{ $labels.cluster }}'
        }
        expression: 'lockbox:kas_proxy_latency:p99_5m > 10'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource lockboxSaturationRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'lockbox-saturation-rules'
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
              'IcM.Description': '#$.annotations.info#'
              'IcM.TsgId': '#$.annotations.runbook_url#'
            }
          }
        ]
        alert: 'UJLockboxHighActiveSessions'
        enabled: true
        labels: {
          severity: 'info'
          slo: 'lockbox-saturation'
        }
        annotations: {
          correlationId: 'UJLockboxSaturation/{{ $labels.cluster }}'
          description: '''More than 10 concurrent breakglass sessions are active. This may
indicate an incident with widespread support access or a session
cleanup issue.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''More than 10 concurrent breakglass sessions are active. This may
indicate an incident with widespread support access or a session
cleanup issue.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-lockbox'
          summary: '[SVC] High number of active breakglass sessions ({{ $value }}) on {{ $labels.cluster }}'
          title: '[SVC] High number of active breakglass sessions ({{ $value }}) on {{ $labels.cluster }}'
        }
        expression: 'lockbox:sessiongate_active_sessions:max > 10'
        for: 'PT15M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
