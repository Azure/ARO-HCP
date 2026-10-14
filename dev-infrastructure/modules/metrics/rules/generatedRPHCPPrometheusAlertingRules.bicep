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
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Resource ID: {{ $labels.resource_id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''Resource ID: {{ $labels.resource_id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m) resource_id:{{ $labels.resource_id }}'
          title: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (1h/5m) resource_id:{{ $labels.resource_id }}'
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
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Resource ID: {{ $labels.resource_id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''Resource ID: {{ $labels.resource_id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m) resource_id:{{ $labels.resource_id }}'
          title: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (6h/30m) resource_id:{{ $labels.resource_id }}'
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
          component: 'slo'
          long_window: '3d'
          severity: '3'
          short_window: '6h'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Resource ID: {{ $labels.resource_id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''Resource ID: {{ $labels.resource_id }}
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable'
          summary: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h) resource_id:{{ $labels.resource_id }}'
          title: '[HCPKASAvailableBurn] {{ $labels.cluster }} / {{ $labels.namespace }} (3d/6h) resource_id:{{ $labels.resource_id }}'
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

resource hcpEtcdGrpcLatencyAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-etcd-grpc-latency-alerts'
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
        alert: 'userJourneyEtcdReadLatencyP991h5m'
        enabled: true
        labels: {
          component: 'etcd'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'etcd-grpc-read-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdReadLatencyP99/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''etcd gRPC Range (read) P99 latency has exceeded 500ms in both the 1h and 5m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''etcd gRPC Range (read) P99 latency has exceeded 500ms in both the 1h and 5m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '[userJourneyEtcdReadLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (1h/5m)'
          title: '[userJourneyEtcdReadLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (1h/5m)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[1h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m]))) > 0.5)'
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
        alert: 'userJourneyEtcdReadLatencyP996h30m'
        enabled: true
        labels: {
          component: 'etcd'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'etcd-grpc-read-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdReadLatencyP99/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''etcd gRPC Range (read) P99 latency has exceeded 500ms in both the 6h and 30m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''etcd gRPC Range (read) P99 latency has exceeded 500ms in both the 6h and 30m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '[userJourneyEtcdReadLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (6h/30m)'
          title: '[userJourneyEtcdReadLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (6h/30m)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[30m]))) > 0.5)'
        for: 'PT5M'
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
        alert: 'userJourneyEtcdReadLatencyP993d6h'
        enabled: true
        labels: {
          component: 'etcd'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
          slo: 'etcd-grpc-read-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdReadLatencyP99/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''etcd gRPC Range (read) P99 latency has exceeded 500ms over 3d/6h windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''etcd gRPC Range (read) P99 latency has exceeded 500ms over 3d/6h windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '[userJourneyEtcdReadLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (3d/6h)'
          title: '[userJourneyEtcdReadLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (3d/6h)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[3d]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5)'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'userJourneyEtcdWriteLatencyP991h5m'
        enabled: true
        labels: {
          component: 'etcd'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'etcd-grpc-write-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWriteLatencyP99/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''etcd gRPC Txn (write) P99 latency has exceeded 500ms in both the 1h and 5m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''etcd gRPC Txn (write) P99 latency has exceeded 500ms in both the 1h and 5m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '[userJourneyEtcdWriteLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (1h/5m)'
          title: '[userJourneyEtcdWriteLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (1h/5m)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[1h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m]))) > 0.5)'
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
        alert: 'userJourneyEtcdWriteLatencyP996h30m'
        enabled: true
        labels: {
          component: 'etcd'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'etcd-grpc-write-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWriteLatencyP99/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''etcd gRPC Txn (write) P99 latency has exceeded 500ms in both the 6h and 30m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''etcd gRPC Txn (write) P99 latency has exceeded 500ms in both the 6h and 30m windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '[userJourneyEtcdWriteLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (6h/30m)'
          title: '[userJourneyEtcdWriteLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (6h/30m)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[30m]))) > 0.5)'
        for: 'PT5M'
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
        alert: 'userJourneyEtcdWriteLatencyP993d6h'
        enabled: true
        labels: {
          component: 'etcd'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
          slo: 'etcd-grpc-write-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWriteLatencyP99/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''etcd gRPC Txn (write) P99 latency has exceeded 500ms over 3d/6h windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''etcd gRPC Txn (write) P99 latency has exceeded 500ms over 3d/6h windows.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: '[userJourneyEtcdWriteLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (3d/6h)'
          title: '[userJourneyEtcdWriteLatencyP99] {{ $labels.cluster }} / {{ $labels.namespace }} P99 > 500ms (3d/6h)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[3d]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le, region) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5)'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource hypershiftJourneyMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-journey-monitor-rules'
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
        alert: 'hypershiftJourneyAvailability1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '5m'
          slo: 'hypershift-journey-availability'
        }
        annotations: {
          correlationId: 'hypershiftJourneyAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Hosted control plane API availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          info: '''Hosted control plane API availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyAvailabilityBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
          title: '[HyperShiftJourneyAvailabilityBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
        }
        expression: '((1 - (hypershift_kube_apiserver_available:sli_sum_1h / hypershift_kube_apiserver_available:sli_count_1h)) > (14.4 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_1h > 54) and on (namespace, cluster) ((1 - (hypershift_kube_apiserver_available:sli_sum_5m / hypershift_kube_apiserver_available:sli_count_5m)) > (14.4 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_5m > 3)'
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
        alert: 'hypershiftJourneyAvailability6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '30m'
          slo: 'hypershift-journey-availability'
        }
        annotations: {
          correlationId: 'hypershiftJourneyAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Hosted control plane API availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          info: '''Hosted control plane API availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyAvailabilityBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
          title: '[HyperShiftJourneyAvailabilityBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
        }
        expression: '((1 - (hypershift_kube_apiserver_available:sli_sum_6h / hypershift_kube_apiserver_available:sli_count_6h)) > (6 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_6h > 64) and on (namespace, cluster) ((1 - (hypershift_kube_apiserver_available:sli_sum_30m / hypershift_kube_apiserver_available:sli_count_30m)) > (6 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_30m > 27)'
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
        alert: 'hypershiftJourneyAvailability3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '6h'
          slo: 'hypershift-journey-availability'
        }
        annotations: {
          correlationId: 'hypershiftJourneyAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Hosted control plane API availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          info: '''Hosted control plane API availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyAvailabilityBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
          title: '[HyperShiftJourneyAvailabilityBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
        }
        expression: '((1 - (hypershift_kube_apiserver_available:sli_sum_3d / hypershift_kube_apiserver_available:sli_count_3d)) > (1 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_3d > 130) and on (namespace, cluster) ((1 - (hypershift_kube_apiserver_available:sli_sum_6h / hypershift_kube_apiserver_available:sli_count_6h)) > (1 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_6h > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftJourneyHealthyClusters1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '5m'
          slo: 'hypershift-journey-errors'
        }
        annotations: {
          correlationId: 'hypershiftJourneyHealthyClusters/{{ $labels.cluster }}'
          description: '''HostedCluster failure-condition share is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''HostedCluster failure-condition share is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyHealthyClustersBurn] {{ $labels.cluster }} (1h/5m)'
          title: '[HyperShiftJourneyHealthyClustersBurn] {{ $labels.cluster }} (1h/5m)'
        }
        expression: '(avg_over_time(hypershift:hostedclusters_unhealthy:ratio[1h]) > (14.4 * (1 - 0.999)) and on (cluster) count_over_time(hypershift:hostedclusters_unhealthy:ratio[1h]) > 54) and on (cluster) (avg_over_time(hypershift:hostedclusters_unhealthy:ratio[5m]) > (14.4 * (1 - 0.999)) and on (cluster) count_over_time(hypershift:hostedclusters_unhealthy:ratio[5m]) > 3)'
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
        alert: 'hypershiftJourneyHealthyClusters6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '30m'
          slo: 'hypershift-journey-errors'
        }
        annotations: {
          correlationId: 'hypershiftJourneyHealthyClusters/{{ $labels.cluster }}'
          description: '''HostedCluster failure-condition share is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''HostedCluster failure-condition share is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyHealthyClustersBurn] {{ $labels.cluster }} (6h/30m)'
          title: '[HyperShiftJourneyHealthyClustersBurn] {{ $labels.cluster }} (6h/30m)'
        }
        expression: '(avg_over_time(hypershift:hostedclusters_unhealthy:ratio[6h]) > (6 * (1 - 0.999)) and on (cluster) count_over_time(hypershift:hostedclusters_unhealthy:ratio[6h]) > 64) and on (cluster) (avg_over_time(hypershift:hostedclusters_unhealthy:ratio[30m]) > (6 * (1 - 0.999)) and on (cluster) count_over_time(hypershift:hostedclusters_unhealthy:ratio[30m]) > 27)'
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
        alert: 'hypershiftJourneyHealthyClusters3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '6h'
          slo: 'hypershift-journey-errors'
        }
        annotations: {
          correlationId: 'hypershiftJourneyHealthyClusters/{{ $labels.cluster }}'
          description: '''HostedCluster failure-condition share is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''HostedCluster failure-condition share is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyHealthyClustersBurn] {{ $labels.cluster }} (3d/6h)'
          title: '[HyperShiftJourneyHealthyClustersBurn] {{ $labels.cluster }} (3d/6h)'
        }
        expression: '(avg_over_time(hypershift:hostedclusters_unhealthy:ratio[3d]) > (1 * (1 - 0.999)) and on (cluster) count_over_time(hypershift:hostedclusters_unhealthy:ratio[3d]) > 130) and on (cluster) (avg_over_time(hypershift:hostedclusters_unhealthy:ratio[6h]) > (1 * (1 - 0.999)) and on (cluster) count_over_time(hypershift:hostedclusters_unhealthy:ratio[6h]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftJourneyCreationLatencySLO'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-journey'
          severity: '3'
          slo: 'hypershift-journey-latency'
        }
        annotations: {
          correlationId: 'hypershiftJourneyCreationLatencySLO/{{ $labels.cluster }}'
          description: '''More than 1% of HostedCluster creations over 30 days took longer than 30 minutes to reach initial availability.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''More than 1% of HostedCluster creations over 30 days took longer than 30 minutes to reach initial availability.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyCreationLatencySLO] {{ $labels.cluster }}'
          title: '[HyperShiftJourneyCreationLatencySLO] {{ $labels.cluster }}'
        }
        expression: 'hypershift:hostedcluster_creation_over_30m:ratio_30d > 0.01'
        for: 'PT1H'
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
        alert: 'hypershiftJourneyClusterWaitingOver30m'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-journey'
          severity: '3'
          slo: 'hypershift-journey-saturation'
        }
        annotations: {
          correlationId: 'hypershiftJourneyClusterWaitingOver30m/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.name }}'
          description: '''HostedCluster has not reached initial availability within 30 minutes.
Management Cluster: {{ $labels.cluster }}
HostedCluster: {{ $labels.namespace }}/{{ $labels.name }}
'''
          info: '''HostedCluster has not reached initial availability within 30 minutes.
Management Cluster: {{ $labels.cluster }}
HostedCluster: {{ $labels.namespace }}/{{ $labels.name }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyClusterWaitingOver30m] {{ $labels.cluster }} / {{ $labels.namespace }}/{{ $labels.name }}'
          title: '[HyperShiftJourneyClusterWaitingOver30m] {{ $labels.cluster }} / {{ $labels.namespace }}/{{ $labels.name }}'
        }
        expression: 'max by (cluster, namespace, name, region) (max without (prometheus_replica) (hypershift_cluster_waiting_initial_availability_duration_seconds)) > 1800'
        for: 'PT5M'
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
        alert: 'hypershiftJourneyTransitionBacklog'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-journey'
          severity: '4'
          slo: 'hypershift-journey-saturation'
        }
        annotations: {
          correlationId: 'hypershiftJourneyTransitionBacklog/{{ $labels.cluster }}'
          description: '''More than 5% of HostedClusters in initial lifecycle transition have exceeded the 30 minute limit.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''More than 5% of HostedClusters in initial lifecycle transition have exceeded the 30 minute limit.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyTransitionBacklog] {{ $labels.cluster }}'
          title: '[HyperShiftJourneyTransitionBacklog] {{ $labels.cluster }}'
        }
        expression: 'hypershift:hostedclusters_waiting_over_30m:ratio > 0.05'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'hypershiftJourneyTrafficDrop'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-journey'
          severity: '4'
          slo: 'hypershift-journey-traffic'
        }
        annotations: {
          correlationId: 'hypershiftJourneyTrafficDrop/{{ $labels.cluster }}'
          description: '''HostedCluster lifecycle transitions dropped below 50% of the rate 24 hours ago.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''HostedCluster lifecycle transitions dropped below 50% of the rate 24 hours ago.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyTrafficDrop] {{ $labels.cluster }}'
          title: '[HyperShiftJourneyTrafficDrop] {{ $labels.cluster }}'
        }
        expression: 'hypershift:hostedcluster_transitions:increase1h < 0.5 * (hypershift:hostedcluster_transitions:increase1h offset 1d) and (hypershift:hostedcluster_transitions:increase1h offset 1d) > 0'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'hypershiftJourneyTrafficSpike'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-journey'
          severity: '4'
          slo: 'hypershift-journey-traffic'
        }
        annotations: {
          correlationId: 'hypershiftJourneyTrafficSpike/{{ $labels.cluster }}'
          description: '''HostedCluster lifecycle transitions exceeded 3x the 7 day average.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''HostedCluster lifecycle transitions exceeded 3x the 7 day average.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftJourneyTrafficSpike] {{ $labels.cluster }}'
          title: '[HyperShiftJourneyTrafficSpike] {{ $labels.cluster }}'
        }
        expression: 'hypershift:hostedcluster_transitions:increase1h > 3 * avg_over_time(hypershift:hostedcluster_transitions:increase1h[1w]) and hypershift:hostedcluster_transitions:increase1h > 10'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource hypershiftOperatorMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-operator-monitor-rules'
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
        alert: 'hypershiftOperatorAvailability1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '5m'
          slo: 'hypershift-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftOperatorAvailability/{{ $labels.cluster }}'
          description: '''hypershift-operator replica availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator replica availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorAvailabilityBurn] {{ $labels.cluster }} (1h/5m)'
          title: '[HyperShiftOperatorAvailabilityBurn] {{ $labels.cluster }} (1h/5m)'
        }
        expression: '((1 - avg_over_time(hypershift:operator:replica_availability:ratio[1h])) > (14.4 * (1 - 0.9995)) and on (cluster) count_over_time(hypershift:operator:replica_availability:ratio[1h]) > 54) and on (cluster) ((1 - avg_over_time(hypershift:operator:replica_availability:ratio[5m])) > (14.4 * (1 - 0.9995)) and on (cluster) count_over_time(hypershift:operator:replica_availability:ratio[5m]) > 3)'
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
        alert: 'hypershiftOperatorAvailability6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '30m'
          slo: 'hypershift-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftOperatorAvailability/{{ $labels.cluster }}'
          description: '''hypershift-operator replica availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator replica availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorAvailabilityBurn] {{ $labels.cluster }} (6h/30m)'
          title: '[HyperShiftOperatorAvailabilityBurn] {{ $labels.cluster }} (6h/30m)'
        }
        expression: '((1 - avg_over_time(hypershift:operator:replica_availability:ratio[6h])) > (6 * (1 - 0.9995)) and on (cluster) count_over_time(hypershift:operator:replica_availability:ratio[6h]) > 64) and on (cluster) ((1 - avg_over_time(hypershift:operator:replica_availability:ratio[30m])) > (6 * (1 - 0.9995)) and on (cluster) count_over_time(hypershift:operator:replica_availability:ratio[30m]) > 27)'
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
        alert: 'hypershiftOperatorAvailability3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '6h'
          slo: 'hypershift-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftOperatorAvailability/{{ $labels.cluster }}'
          description: '''hypershift-operator replica availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator replica availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorAvailabilityBurn] {{ $labels.cluster }} (3d/6h)'
          title: '[HyperShiftOperatorAvailabilityBurn] {{ $labels.cluster }} (3d/6h)'
        }
        expression: '((1 - avg_over_time(hypershift:operator:replica_availability:ratio[3d])) > (1 * (1 - 0.9995)) and on (cluster) count_over_time(hypershift:operator:replica_availability:ratio[3d]) > 130) and on (cluster) ((1 - avg_over_time(hypershift:operator:replica_availability:ratio[6h])) > (1 * (1 - 0.9995)) and on (cluster) count_over_time(hypershift:operator:replica_availability:ratio[6h]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftOperatorReconcileErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '5m'
          slo: 'hypershift-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftOperatorReconcileErrors/{{ $labels.cluster }}'
          description: '''hypershift-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorReconcileErrorsBurn] {{ $labels.cluster }} (1h/5m)'
          title: '[HyperShiftOperatorReconcileErrorsBurn] {{ $labels.cluster }} (1h/5m)'
        }
        expression: '(hypershift:operator:reconcile_error:ratio_rate1h > (14.4 * (1 - 0.999)) and on (cluster) hypershift:operator:reconcile:rate5m > 0) and on (cluster) (hypershift:operator:reconcile_error:ratio_rate5m > (14.4 * (1 - 0.999)) and on (cluster) hypershift:operator:reconcile:rate5m > 0)'
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
        alert: 'hypershiftOperatorReconcileErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '30m'
          slo: 'hypershift-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftOperatorReconcileErrors/{{ $labels.cluster }}'
          description: '''hypershift-operator reconcile error ratio is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator reconcile error ratio is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorReconcileErrorsBurn] {{ $labels.cluster }} (6h/30m)'
          title: '[HyperShiftOperatorReconcileErrorsBurn] {{ $labels.cluster }} (6h/30m)'
        }
        expression: '(hypershift:operator:reconcile_error:ratio_rate6h > (6 * (1 - 0.999)) and on (cluster) hypershift:operator:reconcile:rate5m > 0) and on (cluster) (hypershift:operator:reconcile_error:ratio_rate30m > (6 * (1 - 0.999)) and on (cluster) hypershift:operator:reconcile:rate5m > 0)'
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
        alert: 'hypershiftOperatorReconcileErrors3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '6h'
          slo: 'hypershift-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftOperatorReconcileErrors/{{ $labels.cluster }}'
          description: '''hypershift-operator reconcile error ratio is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator reconcile error ratio is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorReconcileErrorsBurn] {{ $labels.cluster }} (3d/6h)'
          title: '[HyperShiftOperatorReconcileErrorsBurn] {{ $labels.cluster }} (3d/6h)'
        }
        expression: '(hypershift:operator:reconcile_error:ratio_rate3d > (1 * (1 - 0.999)) and on (cluster) hypershift:operator:reconcile:rate5m > 0) and on (cluster) (hypershift:operator:reconcile_error:ratio_rate6h > (1 * (1 - 0.999)) and on (cluster) hypershift:operator:reconcile:rate5m > 0)'
        for: 'PT3H'
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
        alert: 'hypershiftOperatorReconcileLatency1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '5m'
          slo: 'hypershift-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftOperatorReconcileLatency/{{ $labels.cluster }}'
          description: '''hypershift-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorReconcileLatencyBurn] {{ $labels.cluster }} (1h/5m)'
          title: '[HyperShiftOperatorReconcileLatencyBurn] {{ $labels.cluster }} (1h/5m)'
        }
        expression: '(hypershift:operator:reconcile_slow:ratio_rate1h > (14.4 * 0.01) and on (cluster) hypershift:operator:reconcile:rate5m > 0) and on (cluster) (hypershift:operator:reconcile_slow:ratio_rate5m > (14.4 * 0.01) and on (cluster) hypershift:operator:reconcile:rate5m > 0)'
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
        alert: 'hypershiftOperatorReconcileLatency6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '30m'
          slo: 'hypershift-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftOperatorReconcileLatency/{{ $labels.cluster }}'
          description: '''hypershift-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorReconcileLatencyBurn] {{ $labels.cluster }} (6h/30m)'
          title: '[HyperShiftOperatorReconcileLatencyBurn] {{ $labels.cluster }} (6h/30m)'
        }
        expression: '(hypershift:operator:reconcile_slow:ratio_rate6h > (6 * 0.01) and on (cluster) hypershift:operator:reconcile:rate5m > 0) and on (cluster) (hypershift:operator:reconcile_slow:ratio_rate30m > (6 * 0.01) and on (cluster) hypershift:operator:reconcile:rate5m > 0)'
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
        alert: 'hypershiftOperatorReconcileLatency3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '6h'
          slo: 'hypershift-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftOperatorReconcileLatency/{{ $labels.cluster }}'
          description: '''hypershift-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorReconcileLatencyBurn] {{ $labels.cluster }} (3d/6h)'
          title: '[HyperShiftOperatorReconcileLatencyBurn] {{ $labels.cluster }} (3d/6h)'
        }
        expression: '(hypershift:operator:reconcile_slow:ratio_rate3d > (1 * 0.01) and on (cluster) hypershift:operator:reconcile:rate5m > 0) and on (cluster) (hypershift:operator:reconcile_slow:ratio_rate6h > (1 * 0.01) and on (cluster) hypershift:operator:reconcile:rate5m > 0)'
        for: 'PT3H'
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
        alert: 'hypershiftOperatorSaturation1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '5m'
          slo: 'hypershift-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftOperatorSaturation/{{ $labels.cluster }}'
          description: '''hypershift-operator saturated intervals is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator saturated intervals is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorSaturationBurn] {{ $labels.cluster }} (1h/5m)'
          title: '[HyperShiftOperatorSaturationBurn] {{ $labels.cluster }} (1h/5m)'
        }
        expression: '(avg_over_time(hypershift:operator:saturated[1h]) > (14.4 * 0.01) and on (cluster) count_over_time(hypershift:operator:saturated[1h]) > 54) and on (cluster) (avg_over_time(hypershift:operator:saturated[5m]) > (14.4 * 0.01) and on (cluster) count_over_time(hypershift:operator:saturated[5m]) > 3)'
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
        alert: 'hypershiftOperatorSaturation6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '30m'
          slo: 'hypershift-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftOperatorSaturation/{{ $labels.cluster }}'
          description: '''hypershift-operator saturated intervals is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator saturated intervals is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorSaturationBurn] {{ $labels.cluster }} (6h/30m)'
          title: '[HyperShiftOperatorSaturationBurn] {{ $labels.cluster }} (6h/30m)'
        }
        expression: '(avg_over_time(hypershift:operator:saturated[6h]) > (6 * 0.01) and on (cluster) count_over_time(hypershift:operator:saturated[6h]) > 64) and on (cluster) (avg_over_time(hypershift:operator:saturated[30m]) > (6 * 0.01) and on (cluster) count_over_time(hypershift:operator:saturated[30m]) > 27)'
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
        alert: 'hypershiftOperatorSaturation3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'hypershift-operator'
          severity: '3'
          short_window: '6h'
          slo: 'hypershift-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftOperatorSaturation/{{ $labels.cluster }}'
          description: '''hypershift-operator saturated intervals is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator saturated intervals is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorSaturationBurn] {{ $labels.cluster }} (3d/6h)'
          title: '[HyperShiftOperatorSaturationBurn] {{ $labels.cluster }} (3d/6h)'
        }
        expression: '(avg_over_time(hypershift:operator:saturated[3d]) > (1 * 0.01) and on (cluster) count_over_time(hypershift:operator:saturated[3d]) > 130) and on (cluster) (avg_over_time(hypershift:operator:saturated[6h]) > (1 * 0.01) and on (cluster) count_over_time(hypershift:operator:saturated[6h]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftOperatorSaturationSignal'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-operator'
          severity: '4'
          slo: 'hypershift-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftOperatorSaturationSignal/{{ $labels.cluster }}/{{ $labels.signal }}'
          description: '''hypershift-operator {{ $labels.signal }} is above its limit.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator {{ $labels.signal }} is above its limit.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorSaturation] {{ $labels.cluster }} ({{ $labels.signal }})'
          title: '[HyperShiftOperatorSaturation] {{ $labels.cluster }} ({{ $labels.signal }})'
        }
        expression: 'hypershift:operator:saturation_signal == 1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'hypershiftOperatorIdle'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-operator'
          severity: '4'
          slo: 'hypershift-operator-traffic'
        }
        annotations: {
          correlationId: 'hypershiftOperatorIdle/{{ $labels.cluster }}'
          description: '''hypershift-operator has not reconciled anything for 30 minutes.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator has not reconciled anything for 30 minutes.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorIdle] {{ $labels.cluster }}'
          title: '[HyperShiftOperatorIdle] {{ $labels.cluster }}'
        }
        expression: 'hypershift:operator:reconcile:rate5m == 0'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'hypershiftOperatorQueueSurge'
        enabled: true
        labels: {
          component: 'slo'
          service: 'hypershift-operator'
          severity: '4'
          slo: 'hypershift-operator-traffic'
        }
        annotations: {
          correlationId: 'hypershiftOperatorQueueSurge/{{ $labels.cluster }}'
          description: '''hypershift-operator workqueue adds exceed 3x the rate one hour ago.
Management Cluster: {{ $labels.cluster }}
'''
          info: '''hypershift-operator workqueue adds exceed 3x the rate one hour ago.
Management Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftOperatorQueueSurge] {{ $labels.cluster }}'
          title: '[HyperShiftOperatorQueueSurge] {{ $labels.cluster }}'
        }
        expression: 'hypershift:operator:workqueue_adds:rate5m > 3 * (hypershift:operator:workqueue_adds:rate5m offset 1h) and (hypershift:operator:workqueue_adds:rate5m offset 1h) > 0 and hypershift:operator:workqueue_adds:rate5m > 1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource hypershiftCpoMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-cpo-monitor-rules'
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
        alert: 'hypershiftCPOAvailability1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftCPOAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator replica availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator replica availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOAvailabilityBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOAvailabilityBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
        }
        expression: '((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[1h])) > (14.4 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[1h]) > 54) and on (cluster, namespace) ((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[5m])) > (14.4 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[5m]) > 3)'
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
        alert: 'hypershiftCPOAvailability6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftCPOAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator replica availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator replica availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOAvailabilityBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOAvailabilityBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
        }
        expression: '((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[6h])) > (6 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[6h]) > 64) and on (cluster, namespace) ((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[30m])) > (6 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[30m]) > 27)'
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
        alert: 'hypershiftCPOAvailability3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftCPOAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator replica availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator replica availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOAvailabilityBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOAvailabilityBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
        }
        expression: '((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[3d])) > (1 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[3d]) > 130) and on (cluster, namespace) ((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[6h])) > (1 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[6h]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOReconcileErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOReconcileErrorsBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOReconcileErrorsBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
        }
        expression: '(hypershift:cpo:reconcile_error:ratio_rate1h > (14.4 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0) and on (cluster, namespace) (hypershift:cpo:reconcile_error:ratio_rate5m > (14.4 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0)'
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
        alert: 'hypershiftCPOReconcileErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error ratio is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error ratio is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOReconcileErrorsBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOReconcileErrorsBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
        }
        expression: '(hypershift:cpo:reconcile_error:ratio_rate6h > (6 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0) and on (cluster, namespace) (hypershift:cpo:reconcile_error:ratio_rate30m > (6 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0)'
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
        alert: 'hypershiftCPOReconcileErrors3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error ratio is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error ratio is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOReconcileErrorsBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOReconcileErrorsBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
        }
        expression: '(hypershift:cpo:reconcile_error:ratio_rate3d > (1 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0) and on (cluster, namespace) (hypershift:cpo:reconcile_error:ratio_rate6h > (1 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOReconcileLatency1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOReconcileLatencyBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOReconcileLatencyBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
        }
        expression: '(hypershift:cpo:reconcile_slow:ratio_rate1h > (14.4 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0) and on (cluster, namespace) (hypershift:cpo:reconcile_slow:ratio_rate5m > (14.4 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0)'
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
        alert: 'hypershiftCPOReconcileLatency6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOReconcileLatencyBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOReconcileLatencyBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
        }
        expression: '(hypershift:cpo:reconcile_slow:ratio_rate6h > (6 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0) and on (cluster, namespace) (hypershift:cpo:reconcile_slow:ratio_rate30m > (6 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0)'
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
        alert: 'hypershiftCPOReconcileLatency3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOReconcileLatencyBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOReconcileLatencyBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
        }
        expression: '(hypershift:cpo:reconcile_slow:ratio_rate3d > (1 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0) and on (cluster, namespace) (hypershift:cpo:reconcile_slow:ratio_rate6h > (1 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOSaturation1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturation/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator saturated intervals is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator saturated intervals is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOSaturationBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOSaturationBurn] {{ $labels.cluster }} (1h/5m) / {{ $labels.namespace }}'
        }
        expression: '(avg_over_time(hypershift:cpo:saturated[1h]) > (14.4 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[1h]) > 54) and on (cluster, namespace) (avg_over_time(hypershift:cpo:saturated[5m]) > (14.4 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[5m]) > 3)'
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
        alert: 'hypershiftCPOSaturation6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturation/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator saturated intervals is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator saturated intervals is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOSaturationBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOSaturationBurn] {{ $labels.cluster }} (6h/30m) / {{ $labels.namespace }}'
        }
        expression: '(avg_over_time(hypershift:cpo:saturated[6h]) > (6 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[6h]) > 64) and on (cluster, namespace) (avg_over_time(hypershift:cpo:saturated[30m]) > (6 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[30m]) > 27)'
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
        alert: 'hypershiftCPOSaturation3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturation/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator saturated intervals is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator saturated intervals is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOSaturationBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
          title: '[HyperShiftCPOSaturationBurn] {{ $labels.cluster }} (3d/6h) / {{ $labels.namespace }}'
        }
        expression: '(avg_over_time(hypershift:cpo:saturated[3d]) > (1 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[3d]) > 130) and on (cluster, namespace) (avg_over_time(hypershift:cpo:saturated[6h]) > (1 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[6h]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOSaturationSignal'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '4'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturationSignal/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.signal }}'
          description: '''control-plane-operator {{ $labels.signal }} is above its limit.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator {{ $labels.signal }} is above its limit.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOSaturation] {{ $labels.cluster }} / {{ $labels.namespace }} ({{ $labels.signal }})'
          title: '[HyperShiftCPOSaturation] {{ $labels.cluster }} / {{ $labels.namespace }} ({{ $labels.signal }})'
        }
        expression: 'hypershift:cpo:saturation_signal == 1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'hypershiftCPOIdle'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '4'
          slo: 'control-plane-operator-traffic'
        }
        annotations: {
          correlationId: 'hypershiftCPOIdle/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator has not reconciled anything for 30 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator has not reconciled anything for 30 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOIdle] {{ $labels.cluster }} / {{ $labels.namespace }}'
          title: '[HyperShiftCPOIdle] {{ $labels.cluster }} / {{ $labels.namespace }}'
        }
        expression: 'hypershift:cpo:reconcile:rate5m == 0'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'hypershiftCPOQueueSurge'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '4'
          slo: 'control-plane-operator-traffic'
        }
        annotations: {
          correlationId: 'hypershiftCPOQueueSurge/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator workqueue adds exceed 3x the rate one hour ago.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator workqueue adds exceed 3x the rate one hour ago.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '[HyperShiftCPOQueueSurge] {{ $labels.cluster }} / {{ $labels.namespace }}'
          title: '[HyperShiftCPOQueueSurge] {{ $labels.cluster }} / {{ $labels.namespace }}'
        }
        expression: 'hypershift:cpo:workqueue_adds:rate5m > 3 * (hypershift:cpo:workqueue_adds:rate5m offset 1h) and (hypershift:cpo:workqueue_adds:rate5m offset 1h) > 0 and hypershift:cpo:workqueue_adds:rate5m > 1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
