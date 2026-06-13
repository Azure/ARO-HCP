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
        expression: 'avg by (name, namespace, _id, cluster) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30d])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_7d'
        expression: 'avg by (name, namespace, _id, cluster) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[7d])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_1d'
        expression: 'avg by (name, namespace, _id, cluster) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_3d'
        expression: 'avg by (name, namespace, _id, cluster) (avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_30m'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_1h'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_2h'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_6h'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_30m'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1h'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_2h'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_6h'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1d'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_3d'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d])) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_15m'
        expression: 'sum by (name, namespace, _id, cluster) ( sum_over_time( ( hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[15m:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_15m'
        expression: 'sum by (name, namespace, _id, cluster) ( count_over_time( ( max by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[15m:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_6h'
        expression: 'sum by (name, namespace, _id, cluster) ( sum_over_time( ( hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[6h:5m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_6h'
        expression: 'sum by (name, namespace, _id, cluster) ( count_over_time( ( max by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[6h:5m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
    ]
  }
}

resource ujKubeapiserverAvailabilityRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'uj-kubeapiserver-availability-recording-rules'
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
        expression: 'sum by (name, namespace, _id, cluster) ( sum_over_time( ( hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[5m:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_5m'
        expression: 'sum by (name, namespace, _id, cluster) ( count_over_time( ( max by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[5m:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_1h'
        expression: 'sum by (name, namespace, _id, cluster) ( sum_over_time( ( hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[1h:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_1h'
        expression: 'sum by (name, namespace, _id, cluster) ( count_over_time( ( max by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[1h:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_sum_30m'
        expression: 'sum by (name, namespace, _id, cluster) ( sum_over_time( ( hostedClusterAPI_kubeapiserver_available{status="True"} and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[30m:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sli_count_30m'
        expression: 'sum by (name, namespace, _id, cluster) ( count_over_time( ( max by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available) and on (name, namespace, _id, cluster) max by (name, namespace, _id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0) )[30m:1m] ) ) and on (name, namespace, _id, cluster) count by (name, namespace, _id, cluster) (hostedClusterAPI_kubeapiserver_available)'
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
        expression: 'sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m]))'
      }
      {
        record: 'kas:apiserver_request_5xx:rate5m'
        expression: 'sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*", code=~"5.."}[5m]))'
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
        expression: 'sum by (namespace, cluster) ( rate(apiserver_request_sli_duration_seconds_bucket{ namespace=~"ocm-.*", verb=~"POST|PUT|PATCH|DELETE", scope=~"resource|namespace|cluster", subresource!~"proxy|attach|log|exec|portforward", le="1.0" }[5m]) or rate(apiserver_request_sli_duration_seconds_bucket{ namespace=~"ocm-.*", verb=~"GET|LIST", scope="resource", subresource!~"proxy|attach|log|exec|portforward", le="1.0" }[5m]) or rate(apiserver_request_sli_duration_seconds_bucket{ namespace=~"ocm-.*", verb=~"GET|LIST", scope="namespace", subresource!~"proxy|attach|log|exec|portforward", le="5.0" }[5m]) or rate(apiserver_request_sli_duration_seconds_bucket{ namespace=~"ocm-.*", verb=~"GET|LIST", scope="cluster", subresource!~"proxy|attach|log|exec|portforward", le="30.0" }[5m]) ) / sum by (namespace, cluster) ( rate(apiserver_request_sli_duration_seconds_count{ namespace=~"ocm-.*", verb=~"POST|PUT|PATCH|DELETE|GET|LIST", scope=~"resource|namespace|cluster", subresource!~"proxy|attach|log|exec|portforward" }[5m]) )'
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
