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
        expression: 'avg_over_time(probe_success[30d:5m])'
      }
      {
        record: 'probe_availability:ratio_avg_7d'
        expression: 'avg_over_time(probe_success[7d:1m])'
      }
    ]
  }
}

resource hcpKmsRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-kms-recording-rules'
  location: resourceGroup().location
  properties: {
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_30d'
        expression: 'avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30d])'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_7d'
        expression: 'avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[7d])'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_1d'
        expression: 'avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d])'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:ratio_avg_3d'
        expression: 'avg_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d])'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_30m'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_1h'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_2h'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:sum_over_time_6h'
        expression: 'sum by (name, namespace, _id, cluster) (sum_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_30m'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[30m]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1h'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1h]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_2h'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[2h]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_6h'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[6h]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_1d'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[1d]))'
      }
      {
        record: 'hostedClusterAPI_kubeapiserver_available:count_over_time_3d'
        expression: 'sum by (name, namespace, _id, cluster) (count_over_time(hostedClusterAPI_kubeapiserver_available{status="True"}[3d]))'
      }
    ]
  }
}
