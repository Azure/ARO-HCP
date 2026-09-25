#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

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
