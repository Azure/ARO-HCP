#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource kasMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kas-monitor-rules'
  location: resourceGroup().location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.description#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '1h'
          severity: 'critical'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}'
          message: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[5m])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[5m]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[5m])) > 5 and 1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[1h])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1h]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1h])) > 60'
        for: 'PT2M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.description#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'critical'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}'
          message: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[30m])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[30m]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[30m])) > 30 and 1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[6h])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[6h]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[6h])) > 360'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.description#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '1d'
          severity: 'critical'
          short_window: '2h'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}'
          message: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[2h])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[2h]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[2h])) > 120 and 1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[1d])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1d]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1d])) > 1440'
        for: 'PT1H'
        severity: 3
      }
      {
        actions: [
          for g in actionGroups: {
            actionGroupId: g
            actionProperties: {
              'IcM.Title': '#$.labels.cluster#: #$.annotations.description#'
              'IcM.CorrelationId': '#$.annotations.correlationId#'
            }
          }
        ]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'critical'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'kas-monitor-ErrorBudgetBurn/{{ $labels.cluster }}'
          message: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[6h])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[6h]))) > (1 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[6h])) > 360 and 1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[3d])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[3d]))) > (1 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[3d])) > 4320'
        for: 'PT3H'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
