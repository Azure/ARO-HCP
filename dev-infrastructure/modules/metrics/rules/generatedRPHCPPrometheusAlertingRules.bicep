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
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[1h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m]))) > 0.5)'
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
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[30m]))) > 0.5)'
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
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[3d]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Range",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5)'
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
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[1h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[5m]))) > 0.5)'
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
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[30m]))) > 0.5)'
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
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[3d]))) > 0.5) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_method="Txn",grpc_service="etcdserverpb.KV",namespace=~"ocm-.*"}[6h]))) > 0.5)'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource rpEtcdAvailability 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-etcd-availability'
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
        alert: 'userJourneyEtcdBackendCommitDurationHigh1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd backend commit duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd backend commit duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket[5m]))) > 0.1 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket[1h]))) > 0.1) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdBackendCommitDurationHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd backend commit duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd backend commit duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket[30m]))) > 0.1 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket[6h]))) > 0.1) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdBackendCommitDurationHigh3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd backend commit duration is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd backend commit duration is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_backend_commit_duration_seconds_bucket[6h]))) > 0.1) unless on (subscription_id) internal_subscription:info'
        for: 'PT6H'
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
        alert: 'userJourneyEtcdDatabaseHighFragmentation1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseHighFragmentation1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database fragmentation is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd database fragmentation is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '((etcd_mvcc_db_total_size_in_use_in_bytes / etcd_mvcc_db_total_size_in_bytes) < 0.25) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdDatabaseHighFragmentation6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseHighFragmentation6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database fragmentation is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd database fragmentation is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '((etcd_mvcc_db_total_size_in_use_in_bytes / etcd_mvcc_db_total_size_in_bytes) < 0.25) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdDatabaseHighFragmentation3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseHighFragmentation3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database fragmentation is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd database fragmentation is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '((etcd_mvcc_db_total_size_in_use_in_bytes / etcd_mvcc_db_total_size_in_bytes) < 0.25) unless on (subscription_id) internal_subscription:info'
        for: 'PT6H'
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
        alert: 'userJourneyEtcdDatabaseSizeExceeded1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseSizeExceeded1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database size is too large for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd database size is too large for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(etcd_mvcc_db_total_size_in_bytes > 8000000000) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdDatabaseSizeExceeded6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseSizeExceeded6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database size is too large for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd database size is too large for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(etcd_mvcc_db_total_size_in_bytes > 8000000000) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdDatabaseSizeExceeded3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseSizeExceeded3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} database size is {{ $value | humanize }}B (threshold: 8GB). Database may need compaction or quota increase. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database size is chronically too large for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd database size is chronically too large for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(etcd_mvcc_db_total_size_in_bytes > 8000000000) unless on (subscription_id) internal_subscription:info'
        for: 'PT6H'
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
        alert: 'userJourneyEtcdFrequentLeaderChanges1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdFrequentLeaderChanges1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 4). This may indicate network issues or cluster instability. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 4). This may indicate network issues or cluster instability. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(increase(etcd_server_leader_changes_seen_total[15m]) > 4) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdFrequentLeaderChanges6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdFrequentLeaderChanges6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 4). This may indicate network issues or cluster instability. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 4). This may indicate network issues or cluster instability. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd cluster experiencing frequent leader changes for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(increase(etcd_server_leader_changes_seen_total[15m]) > 4) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdFrequentLeaderChanges3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdFrequentLeaderChanges3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 4). This may indicate network issues or cluster instability. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has seen {{ $value }} leader changes in the last 15 minutes (threshold: 4). This may indicate network issues or cluster instability. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd cluster experiencing chronic frequent leader changes for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd cluster experiencing chronic frequent leader changes for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(increase(etcd_server_leader_changes_seen_total[15m]) > 4) unless on (subscription_id) internal_subscription:info'
        for: 'PT6H'
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
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd peer round-trip time is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd peer round-trip time is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket[5m]))) > 0.1 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket[1h]))) > 0.1) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd peer round-trip time is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd peer round-trip time is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket[30m]))) > 0.1 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket[6h]))) > 0.1) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd peer round-trip time is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd peer round-trip time is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_network_peer_round_trip_time_seconds_bucket[6h]))) > 0.1) unless on (subscription_id) internal_subscription:info'
        for: 'PT6H'
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
        alert: 'userJourneyEtcdWALFsyncDurationHigh1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh1h5m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Fast burn (1h/5m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd WAL fsync duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd WAL fsync duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket[5m]))) > 0.05 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket[1h]))) > 0.05) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdWALFsyncDurationHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh6h30m/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Medium burn (6h/30m).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd WAL fsync duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd WAL fsync duration is high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket[30m]))) > 0.05 and histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket[6h]))) > 0.05) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdWALFsyncDurationHigh3d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh3d/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.instance }}'
          description: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Slow burn (3d).'
          info: '{{ $labels.resource_id }} etcd instance {{ $labels.instance }} has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Slow burn (3d).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd WAL fsync duration is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
          title: 'etcd WAL fsync duration is chronically high for {{ $labels.resource_id }} {{ $labels.instance }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, subscription_id, resource_id, instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket[6h]))) > 0.05) unless on (subscription_id) internal_subscription:info'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
