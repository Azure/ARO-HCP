#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource kasMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'kas-monitor-rules'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '1h'
          severity: 'critical'
          short_window: '5m'
        }
        annotations: {
          message: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[5m])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[5m]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[5m])) > 5 and 1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[1h])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1h]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1h])) > 60'
        for: 'PT2M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'critical'
          short_window: '30m'
        }
        annotations: {
          message: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[30m])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[30m]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[30m])) > 30 and 1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[6h])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[6h]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[6h])) > 360'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '1d'
          severity: 'critical'
          short_window: '2h'
        }
        annotations: {
          message: 'High error budget burn for {{ $labels.probe_url }} (current value: {{ $value }})'
        }
        expression: '1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[2h])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[2h]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[2h])) > 120 and 1 - (sum by (probe_url, namespace, _id) (sum_over_time(probe_success{}[1d])) / sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1d]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id) (count_over_time(probe_success{}[1d])) > 1440'
        for: 'PT1H'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'kas-monitor-ErrorBudgetBurn'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'critical'
          short_window: '6h'
        }
        annotations: {
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

resource frontend 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'frontend'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'FrontendLatency'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'The 95th percentile of frontend request latency has exceeded 1 second over the past hour.'
          runbook_url: 'TBD'
          summary: 'Frontend latency is high: 95th percentile exceeds 1 second'
        }
        expression: 'histogram_quantile(0.95, rate(frontend_http_requests_duration_seconds_bucket[1h])) > 1'
        for: 'PT15M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'FrontendClusterServiceErrorRate'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'The Frontend Cluster Service 5xx error rate is above 5% for the last hour. Current value: {{ $value | humanizePercentage }}.'
          runbook_url: 'TBD'
          summary: 'High 4xx|5xx Error Rate on Frontend Cluster Service'
        }
        expression: '(sum(max without(prometheus_replica) (rate(frontend_clusters_service_client_request_count{code=~"4..|5.."}[1h])))) / (sum(max without(prometheus_replica) (rate(frontend_clusters_service_client_request_count[1h])))) > 0.05'
        for: 'PT5M'
        severity: 3
      }
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'FrontendHealthAvailability'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'The Frontend has been unavailable for more than 5 minutes in the last hour.'
          runbook_url: 'TBD'
          summary: 'High unavailability on the Frontend'
        }
        expression: '(1 - (sum_over_time(frontend_health[1h]) / 3600)) >= (300 / 3600)'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource mise 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'mise'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        actions: [for g in actionGroups: { actionGroupId: g }]
        alert: 'MiseEnvoyScrapeDown'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'Prometheus scrape for envoy-stats job in namespace mise is failing or missing.'
          runbook_url: 'TBD'
          summary: 'Envoy scrape target down for namespace=mise'
        }
        expression: 'absent(up{job="envoy-stats", namespace="mise"}) or (up{job="envoy-stats", namespace="mise"} == 0)'
        for: 'PT5M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
