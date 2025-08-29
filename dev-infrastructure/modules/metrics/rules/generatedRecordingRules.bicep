param azureMonitoring string

resource arohcpCsApiAvailabilityRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cs_api_availability_recording_rules'
  location: resourceGroup().location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'availability:api_inbound_request_count:sli_ratio_28d'
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code!~"5..", service="clusters-service-metrics"}[28d]))) / sum by(cluster, namespace, service) ((max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[28d]))))'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'availability:api_inbound_request_count:burnrate5m'
        expression: 'round( ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..|0", service="clusters-service-metrics"}[5m]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[5m]))) ) / (1 - 0.99), 0.01 )'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'availability:api_inbound_request_count:burnrate30m'
        expression: 'round( ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..|0", service="clusters-service-metrics"}[30m]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[30m]))) ) / (1 - 0.99), 0.01 )'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'availability:api_inbound_request_count:burnrate1h'
        expression: 'round( ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..|0", service="clusters-service-metrics"}[1h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[1h]))) ) / (1 - 0.99), 0.01 )'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'availability:api_inbound_request_count:burnrate6h'
        expression: 'round( ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..|0", service="clusters-service-metrics"}[6h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[6h]))) ) / (1 - 0.99), 0.01 )'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'availability:api_inbound_request_count:burnrate3d'
        expression: 'round( ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..|0", service="clusters-service-metrics"}[3d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[3d]))) ) / (1 - 0.99), 0.01 )'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'availability:api_inbound_request_count:burnrate2h'
        expression: 'round( ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..|0", service="clusters-service-metrics"}[2h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[2h]))) ) / (1 - 0.99), 0.01 )'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'availability:api_inbound_request_count:burnrate1d'
        expression: 'round( ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..|0", service="clusters-service-metrics"}[1d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[1d]))) ) / (1 - 0.99), 0.01 )'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
    ]
  }
}

resource arohcpCsApiLatencyRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cs_api_latency_recording_rules'
  location: resourceGroup().location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'latency:api_inbound_request_duration:p99_sli_ratio_28d'
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[28d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[28d])))'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_sli_ratio_28d'
        expression: 'sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[28d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[28d])))'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p99_burnrate5m'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[5m]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[5m]))) ) ) / (1 - 0.99), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p99_burnrate30m'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[30m]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[30m]))) ) ) / (1 - 0.99), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p99_burnrate1h'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[1h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[1h]))) ) ) / (1 - 0.99), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p99_burnrate6h'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[6h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[6h]))) ) ) / (1 - 0.99), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p99_burnrate3d'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[3d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[3d]))) ) ) / (1 - 0.99), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p99_burnrate2h'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[2h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[2h]))) ) ) / (1 - 0.99), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p99_burnrate1d'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="1", code!~"5.."}[1d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[1d]))) ) ) / (1 - 0.99), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_burnrate5m'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[5m]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[5m]))) ) ) / (1 - 0.90), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_burnrate30m'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[30m]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[30m]))) ) ) / (1 - 0.90), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_burnrate1h'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[1h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[1h]))) ) ) / (1 - 0.90), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_burnrate6h'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[6h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[6h]))) ) ) / (1 - 0.90), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_burnrate3d'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[3d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[3d]))) ) ) / (1 - 0.90), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_burnrate2h'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[2h]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[2h]))) ) ) / (1 - 0.90), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
      {
        record: 'latency:api_inbound_request_duration:p90_burnrate1d'
        expression: 'clamp_min( round( ( 1 - ( sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_duration_bucket{namespace="clusters-service", service="clusters-service-metrics", le="0.1", code!~"5.."}[1d]))) / sum by(cluster, namespace, service) (max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code!~"5.."}[1d]))) ) ) / (1 - 0.90), 0.01 ), 0)'
        labels: {
          service: 'clusters-service-metrics'
        }
      }
    ]
  }
}
