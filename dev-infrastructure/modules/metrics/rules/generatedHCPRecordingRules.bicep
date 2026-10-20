param azureMonitoring string

param location string = resourceGroup().location

resource hcpKmsRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-kms-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_30d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_7d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1w])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_1d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_3d'
        expression: 'avg by (name, namespace, _id, resource_id, cluster, region) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_2h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_2h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_3d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_15m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[15m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_15m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[15m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[6h:5m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_6h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[6h:5m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_3d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[3d:30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_3d'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[3d:30m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
    ]
  }
}

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
        record: 'kas:apiserver_request_terminations:rate5m'
        expression: 'sum by (namespace, cluster, region) (rate(apiserver_request_terminations_total{namespace=~"ocm-.*"}[5m]))'
      }
      {
        record: 'kas:apiserver_inflight_requests:avg_5m'
        expression: 'avg_over_time(sum by (namespace, cluster, request_kind, region) (apiserver_current_inflight_requests{namespace=~"ocm-.*"})[5m:1m])'
      }
    ]
  }
}

resource userjourneyKubeapiserverAvailabilityRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'userjourney-kubeapiserver-availability-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_5m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[5m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_5m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[5m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[1h:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_1h'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[1h:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (sum_over_time((hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[30m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_30m'
        expression: 'sum by (name, namespace, _id, resource_id, cluster, region) (count_over_time((max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, resource_id, cluster) max by (name, namespace, _id, resource_id, cluster, region) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0))[30m:1m])) and on (name, namespace, _id, resource_id, cluster) count by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_kubeapiserver_available)'
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

resource hypershiftJourneyAvailabilityRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-journey-availability-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hypershift_kube_apiserver_available:sli_sum_5m'
        expression: 'sum by (namespace, cluster, region) (sum_over_time((max by (namespace, cluster, region) (hypershift_kube_apiserver_available) and on (namespace, cluster) max by (namespace, cluster, region) (hypershift_kube_apiserver_available offset 15m))[5m:1m])) and on (namespace, cluster) count by (namespace, cluster, region) (hypershift_kube_apiserver_available)'
      }
      {
        record: 'hypershift_kube_apiserver_available:sli_count_5m'
        expression: 'sum by (namespace, cluster, region) (count_over_time((max by (namespace, cluster, region) (hypershift_kube_apiserver_available) and on (namespace, cluster) max by (namespace, cluster, region) (hypershift_kube_apiserver_available offset 15m))[5m:1m])) and on (namespace, cluster) count by (namespace, cluster, region) (hypershift_kube_apiserver_available)'
      }
      {
        record: 'hypershift_kube_apiserver_available:sli_sum_30m'
        expression: 'sum by (namespace, cluster, region) (sum_over_time((max by (namespace, cluster, region) (hypershift_kube_apiserver_available) and on (namespace, cluster) max by (namespace, cluster, region) (hypershift_kube_apiserver_available offset 15m))[30m:1m])) and on (namespace, cluster) count by (namespace, cluster, region) (hypershift_kube_apiserver_available)'
      }
      {
        record: 'hypershift_kube_apiserver_available:sli_count_30m'
        expression: 'sum by (namespace, cluster, region) (count_over_time((max by (namespace, cluster, region) (hypershift_kube_apiserver_available) and on (namespace, cluster) max by (namespace, cluster, region) (hypershift_kube_apiserver_available offset 15m))[30m:1m])) and on (namespace, cluster) count by (namespace, cluster, region) (hypershift_kube_apiserver_available)'
      }
      {
        record: 'hypershift_kube_apiserver_available:sli_sum_1h'
        expression: 'sum by (namespace, cluster, region) (sum_over_time((max by (namespace, cluster, region) (hypershift_kube_apiserver_available) and on (namespace, cluster) max by (namespace, cluster, region) (hypershift_kube_apiserver_available offset 15m))[1h:1m])) and on (namespace, cluster) count by (namespace, cluster, region) (hypershift_kube_apiserver_available)'
      }
      {
        record: 'hypershift_kube_apiserver_available:sli_count_1h'
        expression: 'sum by (namespace, cluster, region) (count_over_time((max by (namespace, cluster, region) (hypershift_kube_apiserver_available) and on (namespace, cluster) max by (namespace, cluster, region) (hypershift_kube_apiserver_available offset 15m))[1h:1m])) and on (namespace, cluster) count by (namespace, cluster, region) (hypershift_kube_apiserver_available)'
      }
    ]
  }
}

resource hypershiftCpoRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-cpo-recording-rules'
  location: location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hypershift:cpo:replica_availability:ratio'
        expression: 'clamp_max(max by (cluster, namespace, region) (kube_deployment_status_replicas_available{deployment="control-plane-operator",namespace=~"ocm-.*"}) / (max by (cluster, namespace, region) (kube_deployment_spec_replicas{deployment="control-plane-operator",namespace=~"ocm-.*"}) > 0), 1)'
      }
      {
        record: 'hypershift:cpo:reconcile_error:ratio_rate5m'
        expression: '(sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*",result="error"}[5m]))) or sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[5m]))) * 0) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[5m]))) > 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_slow:ratio_rate5m'
        expression: 'clamp_min(1 - sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_bucket{le="10",namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[5m]))) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_count{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[5m]))) > 0), 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_error:ratio_rate30m'
        expression: '(sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*",result="error"}[30m]))) or sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[30m]))) * 0) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[30m]))) > 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_slow:ratio_rate30m'
        expression: 'clamp_min(1 - sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_bucket{le="10",namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[30m]))) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_count{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[30m]))) > 0), 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_error:ratio_rate1h'
        expression: '(sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*",result="error"}[1h]))) or sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[1h]))) * 0) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[1h]))) > 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_slow:ratio_rate1h'
        expression: 'clamp_min(1 - sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_bucket{le="10",namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[1h]))) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_count{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[1h]))) > 0), 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_error:ratio_rate6h'
        expression: '(sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*",result="error"}[6h]))) or sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[6h]))) * 0) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[6h]))) > 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_slow:ratio_rate6h'
        expression: 'clamp_min(1 - sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_bucket{le="10",namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[6h]))) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_count{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[6h]))) > 0), 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_error:ratio_rate3d'
        expression: '(sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*",result="error"}[3d]))) or sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[3d]))) * 0) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[3d]))) > 0)'
      }
      {
        record: 'hypershift:cpo:reconcile_slow:ratio_rate3d'
        expression: 'clamp_min(1 - sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_bucket{le="10",namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[3d]))) / (sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_time_seconds_count{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[3d]))) > 0), 0)'
      }
      {
        record: 'hypershift:cpo:reconcile:rate5m'
        expression: 'sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(controller_runtime_reconcile_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[5m])))'
      }
      {
        record: 'hypershift:cpo:workqueue_adds:rate5m'
        expression: 'sum by (cluster, namespace, region) (max without (prometheus_replica) (rate(workqueue_adds_total{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"}[5m])))'
      }
      {
        record: 'hypershift:cpo:saturation_signal'
        expression: 'label_replace(max by (cluster, namespace, region) (max without (prometheus_replica) (workqueue_depth{namespace=~"ocm-.*",pod=~"control-plane-operator-.*"})) > bool 100, "signal", "workqueue_depth", "", "")'
      }
      {
        record: 'hypershift:cpo:saturated'
        expression: 'max by (cluster, namespace, region) (hypershift:cpo:saturation_signal)'
      }
    ]
  }
}
