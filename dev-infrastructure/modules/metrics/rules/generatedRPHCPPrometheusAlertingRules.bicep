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

resource arohcpSwiftNetworkingAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_swift_networking_alerts'
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
        alert: 'userJourneySwiftLatencyP991h5m'
        enabled: true
        labels: {
          burn_rate_tier: 'fast'
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneySwiftLatencyP991h5m/{{ $labels.cluster }}'
          description: 'Router pod startup latency p99 has exceeded 300s over the last hour and is still elevated. SWIFT secondary NIC assignment is stalled.'
          info: 'Router pod startup latency p99 has exceeded 300s over the last hour and is still elevated. SWIFT secondary NIC assignment is stalled.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'SWIFT router pod startup latency p99 critically elevated (fast burn)'
          title: 'SWIFT router pod startup latency p99 critically elevated (fast burn)'
        }
        expression: 'router:startup_latency:p99_avg_5m > 300 and router:startup_latency:p99_avg_1h > 300'
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
        alert: 'userJourneySwiftLatencyP996h30m'
        enabled: true
        labels: {
          burn_rate_tier: 'medium'
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneySwiftLatencyP996h30m/{{ $labels.cluster }}'
          description: 'Router pod startup latency p99 has exceeded 300s over the last 6 hours and is still elevated.'
          info: 'Router pod startup latency p99 has exceeded 300s over the last 6 hours and is still elevated.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'SWIFT router pod startup latency p99 elevated (medium burn)'
          title: 'SWIFT router pod startup latency p99 elevated (medium burn)'
        }
        expression: 'router:startup_latency:p99_avg_30m > 300 and router:startup_latency:p99_avg_6h > 300'
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
        alert: 'userJourneySwiftLatencyP993d'
        enabled: true
        labels: {
          burn_rate_tier: 'slow'
          component: 'slo'
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneySwiftLatencyP993d/{{ $labels.cluster }}'
          description: 'Router pod startup latency p99 has been elevated for an extended period. At current rate the monthly SLO budget will be exhausted before end of month.'
          info: 'Router pod startup latency p99 has been elevated for an extended period. At current rate the monthly SLO budget will be exhausted before end of month.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'SWIFT router pod startup latency p99 elevated (slow burn — SLO budget on track to exhaust by month end)'
          title: 'SWIFT router pod startup latency p99 elevated (slow burn — SLO budget on track to exhaust by month end)'
        }
        expression: 'router:startup_latency:p99_avg_6h > 300'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpSwiftKonnectivityAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_swift_konnectivity_alerts'
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
        alert: 'userJourneySwiftKonnectivityErrors1h5m'
        enabled: true
        labels: {
          burn_rate_tier: 'fast'
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneySwiftKonnectivityErrors1h5m/{{ $labels.cluster }}'
          description: 'Konnectivity tunnel stream error rate has exceeded 1% over the last hour and is still elevated. KAS-to-node communication may be degraded.'
          info: 'Konnectivity tunnel stream error rate has exceeded 1% over the last hour and is still elevated. KAS-to-node communication may be degraded.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'Konnectivity tunnel error rate elevated (fast burn)'
          title: 'Konnectivity tunnel error rate elevated (fast burn)'
        }
        expression: 'konnectivity:stream_error_rate:avg_5m > 0.01 and konnectivity:stream_error_rate:avg_1h > 0.01'
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
        alert: 'userJourneySwiftKonnectivityErrors6h30m'
        enabled: true
        labels: {
          burn_rate_tier: 'medium'
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneySwiftKonnectivityErrors6h30m/{{ $labels.cluster }}'
          description: 'Konnectivity tunnel stream error rate has exceeded 1% over the last 6 hours and is still elevated.'
          info: 'Konnectivity tunnel stream error rate has exceeded 1% over the last 6 hours and is still elevated.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'Konnectivity tunnel error rate elevated (medium burn)'
          title: 'Konnectivity tunnel error rate elevated (medium burn)'
        }
        expression: 'konnectivity:stream_error_rate:avg_30m > 0.01 and konnectivity:stream_error_rate:avg_6h > 0.01'
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
        alert: 'userJourneySwiftKonnectivityDialFailures1h5m'
        enabled: true
        labels: {
          burn_rate_tier: 'fast'
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'userJourneySwiftKonnectivityDialFailures1h5m/{{ $labels.cluster }}'
          description: 'Konnectivity server dial failure rate has exceeded 1%. KAS cannot establish connections to worker nodes.'
          info: 'Konnectivity server dial failure rate has exceeded 1%. KAS cannot establish connections to worker nodes.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'Konnectivity dial failure rate elevated (fast burn)'
          title: 'Konnectivity dial failure rate elevated (fast burn)'
        }
        expression: 'konnectivity:dial_failure_rate:avg_5m > 0.01 and konnectivity:dial_failure_rate:avg_1h > 0.01'
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
        alert: 'userJourneySwiftKonnectivityDialFailures6h30m'
        enabled: true
        labels: {
          burn_rate_tier: 'medium'
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'userJourneySwiftKonnectivityDialFailures6h30m/{{ $labels.cluster }}'
          description: 'Konnectivity server dial failure rate has exceeded 1% over the last 6 hours.'
          info: 'Konnectivity server dial failure rate has exceeded 1% over the last 6 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-swift'
          summary: 'Konnectivity dial failure rate elevated (medium burn)'
          title: 'Konnectivity dial failure rate elevated (medium burn)'
        }
        expression: 'konnectivity:dial_failure_rate:avg_30m > 0.01 and konnectivity:dial_failure_rate:avg_6h > 0.01'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
