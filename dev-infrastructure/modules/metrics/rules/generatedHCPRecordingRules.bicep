param azureMonitoring string

param location string = resourceGroup().location

resource hcpKasApiserverRequestRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-kas-apiserver-request-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'kas:apiserver_request_total:rate5m'
        expression: 'sum by (namespace, cluster, region) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m]))'
      }
      {
        record: 'kas:apiserver_request_5xx:rate5m'
        expression: 'sum by (namespace, cluster, region) (rate(apiserver_request_total{code=~"5..",namespace=~"ocm-.*"}[5m]))'
      }
      {
        record: 'kas:apiserver_request_total:rate_avg_30d'
        expression: 'avg_over_time(kas:apiserver_request_total:rate5m[30d:5m])'
      }
      {
        record: 'kas:apiserver_request_5xx:rate_avg_30d'
        expression: 'avg_over_time(kas:apiserver_request_5xx:rate5m[30d:5m])'
      }
      {
        record: 'kas:apiserver_request_total:rate_avg_1h'
        expression: 'avg_over_time(kas:apiserver_request_total:rate5m[1h])'
      }
      {
        record: 'kas:apiserver_request_5xx:rate_avg_1h'
        expression: 'avg_over_time(kas:apiserver_request_5xx:rate5m[1h])'
      }
      {
        record: 'kas:apiserver_request_total:rate_avg_6h'
        expression: 'avg_over_time(kas:apiserver_request_total:rate5m[6h])'
      }
      {
        record: 'kas:apiserver_request_5xx:rate_avg_6h'
        expression: 'avg_over_time(kas:apiserver_request_5xx:rate5m[6h])'
      }
      {
        record: 'kas:apiserver_request_total:rate_avg_3d'
        expression: 'avg_over_time(kas:apiserver_request_total:rate5m[3d])'
      }
      {
        record: 'kas:apiserver_request_5xx:rate_avg_3d'
        expression: 'avg_over_time(kas:apiserver_request_5xx:rate5m[3d])'
      }
      {
        record: 'kas:apiserver_request_total:rate_avg_5m'
        expression: 'avg_over_time(kas:apiserver_request_total:rate5m[5m])'
      }
      {
        record: 'kas:apiserver_request_5xx:rate_avg_5m'
        expression: 'avg_over_time(kas:apiserver_request_5xx:rate5m[5m])'
      }
      {
        record: 'kas:apiserver_request_total:rate_avg_30m'
        expression: 'avg_over_time(kas:apiserver_request_total:rate5m[30m])'
      }
      {
        record: 'kas:apiserver_request_5xx:rate_avg_30m'
        expression: 'avg_over_time(kas:apiserver_request_5xx:rate5m[30m])'
      }
    ]
  }
}

resource hcpKasLatencyRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-kas-latency-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'kas:apiserver_request_latency:sli_ratio:rate5m'
        expression: 'sum by (namespace, cluster, region) (rate(apiserver_request_sli_duration_seconds_bucket{le="1.0",namespace=~"ocm-.*",scope=~"resource|namespace|cluster",subresource!~"proxy|attach|log|exec|portforward",verb=~"POST|PUT|PATCH|DELETE"}[5m]) or rate(apiserver_request_sli_duration_seconds_bucket{le="1.0",namespace=~"ocm-.*",scope="resource",subresource!~"proxy|attach|log|exec|portforward",verb=~"GET|LIST"}[5m]) or rate(apiserver_request_sli_duration_seconds_bucket{le="5.0",namespace=~"ocm-.*",scope="namespace",subresource!~"proxy|attach|log|exec|portforward",verb=~"GET|LIST"}[5m]) or rate(apiserver_request_sli_duration_seconds_bucket{le="30.0",namespace=~"ocm-.*",scope="cluster",subresource!~"proxy|attach|log|exec|portforward",verb=~"GET|LIST"}[5m])) / sum by (namespace, cluster, region) (rate(apiserver_request_sli_duration_seconds_count{namespace=~"ocm-.*",scope=~"resource|namespace|cluster",subresource!~"proxy|attach|log|exec|portforward",verb=~"POST|PUT|PATCH|DELETE|GET|LIST"}[5m]))'
      }
      {
        record: 'kas:apiserver_request_latency:sli_ratio:rate_avg_30m'
        expression: 'avg_over_time(kas:apiserver_request_latency:sli_ratio:rate5m[30m])'
      }
      {
        record: 'kas:apiserver_request_latency:sli_ratio:rate_avg_1h'
        expression: 'avg_over_time(kas:apiserver_request_latency:sli_ratio:rate5m[1h])'
      }
      {
        record: 'kas:apiserver_request_latency:sli_ratio:rate_avg_6h'
        expression: 'avg_over_time(kas:apiserver_request_latency:sli_ratio:rate5m[6h])'
      }
      {
        record: 'kas:apiserver_request_latency:sli_ratio:rate_avg_3d'
        expression: 'avg_over_time(kas:apiserver_request_latency:sli_ratio:rate5m[3d])'
      }
      {
        record: 'kas:apiserver_request_latency:sli_ratio:rate_avg_30d'
        expression: 'avg_over_time(kas:apiserver_request_latency:sli_ratio:rate5m[30d:5m])'
      }
    ]
  }
}

resource hcpEtcdGrpcLatencyRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-etcd-grpc-latency-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'etcd:grpc_server_handling:read_latency_p99:rate5m'
        expression: 'histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m])))'
      }
      {
        record: 'etcd:grpc_server_handling:read_latency_p95:rate5m'
        expression: 'histogram_quantile(0.95, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m])))'
      }
      {
        record: 'etcd:grpc_server_handling:write_latency_p99:rate5m'
        expression: 'histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m])))'
      }
      {
        record: 'etcd:grpc_server_handling:write_latency_p95:rate5m'
        expression: 'histogram_quantile(0.95, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m])))'
      }
    ]
  }
}

resource arohcpIngressAvailabilitySloRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_ingress_availability_slo_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'availability:ingress_canary:ratio'
        expression: 'sum by (_id, cluster, region) (ingress_canary_route_reachable) / count by (_id, cluster, region) (ingress_canary_route_reachable)'
      }
      {
        record: 'errors:ingress_canary:error_rate'
        expression: '1 - availability:ingress_canary:ratio'
      }
    ]
  }
}

resource arohcpIngressLatencySloRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_ingress_latency_slo_recording_rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'latency:ingress_canary:ratio'
        expression: 'sum by (_id, cluster, region) (rate(ingress_canary_check_duration_bucket{le="200"}[5m])) / sum by (_id, cluster, region) (rate(ingress_canary_check_duration_bucket{le="+Inf"}[5m]))'
      }
      {
        record: 'errors:ingress_canary_latency:error_rate'
        expression: '1 - latency:ingress_canary:ratio'
      }
    ]
  }
}
