param azureMonitoring string

resource kasMonitorRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kas-monitor-recording-rules'
  location: resourceGroup().location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'probe_availability:ratio_avg_30d'
        expression: 'avg_over_time(probe_success[30d])'
      }
    ]
  }
}
