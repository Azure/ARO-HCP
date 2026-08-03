#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource rpUserjourneyEtcdLatencyMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-userjourney-etcd-latency-monitor-rules'
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
        alert: 'userJourneyEtcdLatencyP991h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'etcd-grpc-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdLatencyP991h5m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'etcd gRPC P99 latency in namespace {{ $labels.namespace }} has exceeded 200ms in both the 1h and 5m windows, indicating a fast burn-rate degradation affecting hosted cluster operations.'
          info: 'etcd gRPC P99 latency in namespace {{ $labels.namespace }} has exceeded 200ms in both the 1h and 5m windows, indicating a fast burn-rate degradation affecting hosted cluster operations.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd gRPC P99 latency exceeds 200ms in {{ $labels.namespace }} (fast burn 1h/5m)'
          title: 'etcd gRPC P99 latency exceeds 200ms in {{ $labels.namespace }} (fast burn 1h/5m)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[1h]))) > 0.2) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[5m]))) > 0.2)'
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
        alert: 'userJourneyEtcdLatencyP996h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'etcd-grpc-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdLatencyP996h30m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'etcd gRPC P99 latency in namespace {{ $labels.namespace }} has exceeded 200ms in both the 6h and 30m windows, indicating a sustained medium burn-rate degradation affecting hosted cluster operations.'
          info: 'etcd gRPC P99 latency in namespace {{ $labels.namespace }} has exceeded 200ms in both the 6h and 30m windows, indicating a sustained medium burn-rate degradation affecting hosted cluster operations.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd gRPC P99 latency exceeds 200ms in {{ $labels.namespace }} (medium burn 6h/30m)'
          title: 'etcd gRPC P99 latency exceeds 200ms in {{ $labels.namespace }} (medium burn 6h/30m)'
        }
        expression: '(histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[6h]))) > 0.2) and (histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[30m]))) > 0.2)'
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
        alert: 'userJourneyEtcdLatencyP993d'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          slo: 'etcd-grpc-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdLatencyP993d/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'etcd gRPC P99 latency in namespace {{ $labels.namespace }} has exceeded 200ms over a 3d window, indicating a slow but persistent latency degradation. This generates a ticket for investigation.'
          info: 'etcd gRPC P99 latency in namespace {{ $labels.namespace }} has exceeded 200ms over a 3d window, indicating a slow but persistent latency degradation. This generates a ticket for investigation.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd gRPC P99 latency exceeds 200ms in {{ $labels.namespace }} (slow burn 3d)'
          title: 'etcd gRPC P99 latency exceeds 200ms in {{ $labels.namespace }} (slow burn 3d)'
        }
        expression: 'histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[3d]))) > 0.2'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
