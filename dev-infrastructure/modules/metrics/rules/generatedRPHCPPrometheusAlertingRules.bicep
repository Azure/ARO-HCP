#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource arohcpSwiftNetworkingAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_swift_networking_alerts'
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
        alert: 'userJourneySwiftLatencyP991h5m'
        enabled: true
        labels: {
          burn_rate_tier: 'fast'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneySwiftLatencyP991h5m/{{ $labels.cluster }}'
          description: 'Router pod startup latency p99 has exceeded 300s over the last hour and is still elevated. SWIFT secondary NIC assignment is stalled.'
          info: 'Router pod startup latency p99 has exceeded 300s over the last hour and is still elevated. SWIFT secondary NIC assignment is stalled.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'SWIFT router pod startup latency p99 critically elevated (fast burn)'
          title: 'SWIFT router pod startup latency p99 critically elevated (fast burn)'
        }
        expression: 'router:startup_latency:p99_avg_5m > 300 and router:startup_latency:p99_avg_1h > 300'
        for: 'PT2M'
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
        alert: 'userJourneySwiftLatencyP996h30m'
        enabled: true
        labels: {
          burn_rate_tier: 'medium'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneySwiftLatencyP996h30m/{{ $labels.cluster }}'
          description: 'Router pod startup latency p99 has exceeded 300s over the last 6 hours and is still elevated.'
          info: 'Router pod startup latency p99 has exceeded 300s over the last 6 hours and is still elevated.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'SWIFT router pod startup latency p99 elevated (medium burn)'
          title: 'SWIFT router pod startup latency p99 elevated (medium burn)'
        }
        expression: 'router:startup_latency:p99_avg_30m > 300 and router:startup_latency:p99_avg_6h > 300'
        for: 'PT15M'
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
        alert: 'userJourneySwiftLatencyP993d'
        enabled: true
        labels: {
          burn_rate_tier: 'slow'
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneySwiftLatencyP993d/{{ $labels.cluster }}'
          description: 'Router pod startup latency p99 has been elevated for an extended period. At current rate the monthly SLO budget will be exhausted before end of month.'
          info: 'Router pod startup latency p99 has been elevated for an extended period. At current rate the monthly SLO budget will be exhausted before end of month.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'SWIFT router pod startup latency p99 elevated (slow burn — SLO budget on track to exhaust by month end)'
          title: 'SWIFT router pod startup latency p99 elevated (slow burn — SLO budget on track to exhaust by month end)'
        }
        expression: 'router:startup_latency:p99_avg_6h > 300'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
