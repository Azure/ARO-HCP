#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource rpUjKasAvailabilityMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-uj-kas-availability-monitor-rules'
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
        alert: 'UJKubeApiserverAvailability1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: 'critical'
          short_window: '5m'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'UJKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels._id }}'
          description: '''Burn rate: fast (14.4x, 1h/5m, for: 10m)
CID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}/{{ $labels.namespace }}
'''
          info: '''Burn rate: fast (14.4x, 1h/5m, for: 10m)
CID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}/{{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: 'KubeAPIServer availability error budget burn ({{ $labels._id }})'
          title: 'KubeAPIServer availability error budget burn ({{ $labels._id }})'
        }
        expression: '( 1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_5m / hostedClusterAPI_kubeapiserver_available:sli_count_5m) > (14.4 * (1 - 0.9995)) and on (name, namespace, _id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_5m > 3 ) and on (name, namespace, _id, cluster) ( 1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_1h / hostedClusterAPI_kubeapiserver_available:sli_count_1h) > (14.4 * (1 - 0.9995)) and on (name, namespace, _id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_1h > 54 )'
        for: 'PT10M'
        severity: 2
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
        alert: 'UJKubeApiserverAvailability6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'critical'
          short_window: '30m'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'UJKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels._id }}'
          description: '''Burn rate: medium (6x, 6h/30m, for: 30m)
CID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}/{{ $labels.namespace }}
'''
          info: '''Burn rate: medium (6x, 6h/30m, for: 30m)
CID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}/{{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: 'KubeAPIServer availability error budget burn ({{ $labels._id }})'
          title: 'KubeAPIServer availability error budget burn ({{ $labels._id }})'
        }
        expression: '( 1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_30m / hostedClusterAPI_kubeapiserver_available:sli_count_30m) > (6 * (1 - 0.9995)) and on (name, namespace, _id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_30m > 27 ) and on (name, namespace, _id, cluster) ( 1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_6h / hostedClusterAPI_kubeapiserver_available:sli_count_6h) > (6 * (1 - 0.9995)) and on (name, namespace, _id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_6h > 64 )'
        for: 'PT30M'
        severity: 2
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
        alert: 'UJKubeApiserverAvailability3d6h'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'warning'
          short_window: '6h'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'UJKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels._id }}'
          description: '''Burn rate: slow (1x, 3d/6h, for: 3h)
CID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}/{{ $labels.namespace }}
'''
          info: '''Burn rate: slow (1x, 3d/6h, for: 3h)
CID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}/{{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: 'KubeAPIServer availability error budget burn ({{ $labels._id }})'
          title: 'KubeAPIServer availability error budget burn ({{ $labels._id }})'
        }
        expression: '( 1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_6h / hostedClusterAPI_kubeapiserver_available:sli_count_6h) > (1 * (1 - 0.9995)) and on (name, namespace, _id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_6h > 64 ) and on (name, namespace, _id, cluster) ( 1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_3d / hostedClusterAPI_kubeapiserver_available:sli_count_3d) > (1 * (1 - 0.9995)) and on (name, namespace, _id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_3d > 130 )'
        for: 'PT3H'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
