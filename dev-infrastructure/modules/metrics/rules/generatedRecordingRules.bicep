param azureMonitoring string

param location string = resourceGroup().location

resource lockboxRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'lockbox-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'lockbox:sessiongate_active_sessions:max'
        expression: 'max by (cluster) (sessiongate_active_sessions)'
      }
      {
        record: 'lockbox:kas_proxy_requests_total:rate5m'
        expression: 'sum by (cluster) (rate(sessiongate_kas_proxy_requests_total[5m]))'
      }
      {
        record: 'lockbox:kas_proxy_errors_total:rate5m'
        expression: 'sum by (cluster) (rate(sessiongate_kas_proxy_requests_total{status!~"2.."}[5m]))'
      }
      {
        record: 'lockbox:kas_proxy_latency:avg_5m'
        expression: 'sum by (cluster) (rate(sessiongate_kas_proxy_requests_duration_seconds_sum[5m])) / sum by (cluster) (rate(sessiongate_kas_proxy_requests_duration_seconds_count[5m]))'
      }
      {
        record: 'lockbox:audit_log_error_rate:ratio_1h'
        expression: '( sum by (cluster) (rate(otel_audit_log_send_errors_total{job="aro-hcp-admin-api-metrics"}[1h])) / sum by (cluster) (rate(otel_audit_log_records_total{job="aro-hcp-admin-api-metrics"}[1h])) )'
      }
      {
        record: 'lockbox:audit_log_connection_degraded:max'
        expression: 'max by (cluster) (otel_audit_log_connection_degraded{job="aro-hcp-admin-api-metrics"})'
      }
      {
        record: 'lockbox:audit_log_records:rate5m'
        expression: 'sum by (cluster) (rate(otel_audit_log_records_total{job="aro-hcp-admin-api-metrics"}[5m]))'
      }
    ]
  }
}
