#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource rpUserjourneyKasAvailabilityMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-userjourney-kas-availability-monitor-rules'
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
        alert: 'userJourneyKubeApiserverAvailability1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels._id }}'
          description: '''Resource ID: {{ $labels.resource_id }}
OCM Cluster ID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''Resource ID: {{ $labels.resource_id }}
OCM Cluster ID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m)'
          title: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m) resource_id:{{ $labels.resource_id }} _id:{{ $labels._id }}'
        }
        expression: '(1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_5m / hostedClusterAPI_kubeapiserver_available:sli_count_5m) > (14.4 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_5m > 3) and on (name, namespace, _id, resource_id, cluster) (1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_1h / hostedClusterAPI_kubeapiserver_available:sli_count_1h) > (14.4 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_1h > 54)'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'userJourneyKubeApiserverAvailability6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels._id }}'
          description: '''Resource ID: {{ $labels.resource_id }}
OCM Cluster ID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''Resource ID: {{ $labels.resource_id }}
OCM Cluster ID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m)'
          title: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m) resource_id:{{ $labels.resource_id }} _id:{{ $labels._id }}'
        }
        expression: '(1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_30m / hostedClusterAPI_kubeapiserver_available:sli_count_30m) > (6 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_30m > 27) and on (name, namespace, _id, resource_id, cluster) (1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_6h / hostedClusterAPI_kubeapiserver_available:sli_count_6h) > (6 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_6h > 64)'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'userJourneyKubeApiserverAvailability3d6h'
        enabled: true
        labels: {
          long_window: '3d'
          severity: '3'
          short_window: '6h'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels._id }}'
          description: '''Resource ID: {{ $labels.resource_id }}
OCM Cluster ID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''Resource ID: {{ $labels.resource_id }}
OCM Cluster ID: {{ $labels._id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h)'
          title: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h) resource_id:{{ $labels.resource_id }} _id:{{ $labels._id }}'
        }
        expression: '(1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_6h / hostedClusterAPI_kubeapiserver_available:sli_count_6h) > (1 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_6h > 64) and on (name, namespace, _id, resource_id, cluster) (1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_3d / hostedClusterAPI_kubeapiserver_available:sli_count_3d) > (1 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_3d > 130)'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource rpUserjourneyKasErrorsMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-userjourney-kas-errors-monitor-rules'
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
        alert: 'userJourneyKubeApiserverErrors1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'kas-errors'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          info: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASErrorsBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m)'
          title: '[HCPKASErrorsBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m)'
        }
        expression: '(kas:apiserver_request_5xx:rate_avg_5m / kas:apiserver_request_total:rate_avg_5m > (14.4 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_5m > 1) and on (namespace, cluster) (kas:apiserver_request_5xx:rate_avg_1h / kas:apiserver_request_total:rate_avg_1h > (14.4 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_1h > 1) and on (namespace, cluster) (sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m] offset 15m)) > 0)'
        for: 'PT2M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'userJourneyKubeApiserverErrors6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'kas-errors'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          info: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASErrorsBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m)'
          title: '[HCPKASErrorsBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m)'
        }
        expression: '(kas:apiserver_request_5xx:rate_avg_30m / kas:apiserver_request_total:rate_avg_30m > (6 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_30m > 1) and on (namespace, cluster) (kas:apiserver_request_5xx:rate_avg_6h / kas:apiserver_request_total:rate_avg_6h > (6 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_6h > 1) and on (namespace, cluster) (sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m] offset 15m)) > 0)'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'userJourneyKubeApiserverErrors3d6h'
        enabled: true
        labels: {
          long_window: '3d'
          severity: '3'
          short_window: '6h'
          slo: 'kas-errors'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          info: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASErrorsBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h)'
          title: '[HCPKASErrorsBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h)'
        }
        expression: '(kas:apiserver_request_5xx:rate_avg_6h / kas:apiserver_request_total:rate_avg_6h > (1 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_6h > 1) and on (namespace, cluster) (kas:apiserver_request_5xx:rate_avg_3d / kas:apiserver_request_total:rate_avg_3d > (1 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_3d > 1) and on (namespace, cluster) (sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m] offset 15m)) > 0)'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource rpUserjourneyKasLatencyMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-userjourney-kas-latency-monitor-rules'
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
        alert: 'userJourneyKubeApiserverLatency1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'kas-latency'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          info: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASLatencyBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m)'
          title: '[HCPKASLatencyBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m)'
        }
        expression: '((1 - kas:apiserver_request_latency:sli_ratio:rate5m) > (14.4 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_5m > 1) and on (namespace, cluster) ((1 - kas:apiserver_request_latency:sli_ratio:rate_avg_1h) > (14.4 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_1h > 1) and on (namespace, cluster) (sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m] offset 15m)) > 0)'
        for: 'PT2M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'userJourneyKubeApiserverLatency6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'kas-latency'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          info: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASLatencyBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m)'
          title: '[HCPKASLatencyBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m)'
        }
        expression: '((1 - kas:apiserver_request_latency:sli_ratio:rate_avg_30m) > (6 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_30m > 1) and on (namespace, cluster) ((1 - kas:apiserver_request_latency:sli_ratio:rate_avg_6h) > (6 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_6h > 1) and on (namespace, cluster) (sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m] offset 15m)) > 0)'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
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
        alert: 'userJourneyKubeApiserverLatency3d6h'
        enabled: true
        labels: {
          long_window: '3d'
          severity: '3'
          short_window: '6h'
          slo: 'kas-latency'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          info: '''Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
Lookup: use hcpctl to map namespace to cluster resource ID.
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASLatencyBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h)'
          title: '[HCPKASLatencyBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h)'
        }
        expression: '((1 - kas:apiserver_request_latency:sli_ratio:rate_avg_6h) > (1 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_6h > 1) and on (namespace, cluster) ((1 - kas:apiserver_request_latency:sli_ratio:rate_avg_3d) > (1 * (1 - 0.9995)) and on (namespace, cluster) kas:apiserver_request_total:rate_avg_3d > 1) and on (namespace, cluster) (sum by (namespace, cluster) (rate(apiserver_request_total{namespace=~"ocm-.*"}[5m] offset 15m)) > 0)'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
