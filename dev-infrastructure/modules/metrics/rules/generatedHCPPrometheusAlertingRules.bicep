#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource mgmtCapacityRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'mgmt-capacity-rules'
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
        alert: 'MgmtClusterHCPCapacityWarning'
        enabled: true
        labels: {
          component: 'capacity'
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityWarning/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          info: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds warning threshold of 60%.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster {{ $labels.cluster }} HCP capacity approaching limit (60% threshold)'
          title: 'Management cluster {{ $labels.cluster }} HCP capacity approaching limit (60% threshold)'
        }
        expression: '(count by (cluster) (kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) / 60) > 0.6'
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
        alert: 'MgmtClusterNodeSwiftNICCapacityZero'
        enabled: true
        labels: {
          component: 'capacity'
          severity: 'critical'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterNodeSwiftNICCapacityZero/{{ $labels.cluster }}/{{ $labels.node }}'
          description: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity. No HCPs can be scheduled on this node until NIC capacity is restored.'
          info: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity. No HCPs can be scheduled on this node until NIC capacity is restored.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://portal.microsofticm.com/imp/v5/incidents/details/802529667'
          summary: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity'
          title: 'Node {{ $labels.node }} on management cluster {{ $labels.cluster }} has zero SWIFT NIC capacity'
        }
        expression: 'kube_node_status_capacity{node=~"user.*",resource="aro_openshift_io_swift_nic"} == 0'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(2, severityCeiling) : 2
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
        alert: 'MgmtClusterHCPCapacityCritical'
        enabled: true
        labels: {
          component: 'capacity'
          severity: 'info'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtClusterHCPCapacityCritical/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          info: 'Management cluster {{ $labels.cluster }} is at {{ $value | humanizePercentage }} of its HCP capacity (60 HCP limit). Current count exceeds critical threshold of 85%. Immediate action required to provision additional management cluster capacity.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Management cluster {{ $labels.cluster }} HCP capacity critically high (85% threshold)'
          title: 'Management cluster {{ $labels.cluster }} HCP capacity critically high (85% threshold)'
        }
        expression: '(count by (cluster) (kube_namespace_labels{namespace=~"^ocm-[^-]+-[^-]+$"}) / 60) > 0.85'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
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
        alert: 'userJourneyEtcdLatencyP991h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'etcd-grpc-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdLatencyP991h5m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd P99 gRPC latency has exceeded 200ms in both the 1h and 5m windows, indicating a fast error budget burn (14.4x). At this rate, the latency SLO budget will be exhausted in ~3 days. Common causes: disk I/O saturation, CPU throttling, network latency between etcd members, or large range queries.'
          info: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd P99 gRPC latency has exceeded 200ms in both the 1h and 5m windows, indicating a fast error budget burn (14.4x). At this rate, the latency SLO budget will be exhausted in ~3 days. Common causes: disk I/O saturation, CPU throttling, network latency between etcd members, or large range queries.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd P99 gRPC latency critically high (>200ms, fast burn)'
          title: 'etcd P99 gRPC latency critically high (>200ms, fast burn) namespace:{{ $labels.namespace }} cluster:{{ $labels.cluster }}'
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
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'etcd-grpc-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdLatencyP996h30m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd P99 gRPC latency has exceeded 200ms in both the 6h and 30m windows, indicating a medium error budget burn (6x). At this rate, the latency SLO budget will be exhausted in ~5 days. Investigate disk I/O performance, etcd resource utilization, database size, and recent workload changes.'
          info: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd P99 gRPC latency has exceeded 200ms in both the 6h and 30m windows, indicating a medium error budget burn (6x). At this rate, the latency SLO budget will be exhausted in ~5 days. Investigate disk I/O performance, etcd resource utilization, database size, and recent workload changes.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd P99 gRPC latency elevated (>200ms, medium burn)'
          title: 'etcd P99 gRPC latency elevated (>200ms, medium burn) namespace:{{ $labels.namespace }} cluster:{{ $labels.cluster }}'
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
          long_window: '3d'
          severity: '4'
          slo: 'etcd-grpc-latency'
        }
        annotations: {
          correlationId: 'userJourneyEtcdLatencyP993d/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd P99 gRPC latency has exceeded 200ms over a 3-day window, indicating persistent degradation at the SLO boundary (1x burn rate). At this rate, the monthly latency SLO budget will be exhausted by end of month. This typically indicates gradual performance degradation from growing database size, slow disk, or increasing cluster load.'
          info: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd P99 gRPC latency has exceeded 200ms over a 3-day window, indicating persistent degradation at the SLO boundary (1x burn rate). At this rate, the monthly latency SLO budget will be exhausted by end of month. This typically indicates gradual performance degradation from growing database size, slow disk, or increasing cluster load.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd P99 gRPC latency exceeds SLO (>200ms, slow burn)'
          title: 'etcd P99 gRPC latency exceeds SLO (>200ms, slow burn) namespace:{{ $labels.namespace }} cluster:{{ $labels.cluster }}'
        }
        expression: 'histogram_quantile(0.99, sum by (namespace, cluster, le) (rate(grpc_server_handling_seconds_bucket{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[3d]))) > 0.2'
        for: 'PT10M'
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
        alert: 'userJourneyEtcdErrors1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'etcd-grpc-errors'
        }
        annotations: {
          correlationId: 'userJourneyEtcdErrors1h5m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd gRPC error rate has exceeded 0.72% (14.4x burn rate against 99.95% SLO) in both the 1h and 5m windows. At this rate, the monthly error budget (0.05%) will be exhausted in ~3 days. Check for etcd member failures, network partitions, or resource exhaustion (Unavailable/DeadlineExceeded gRPC codes).'
          info: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd gRPC error rate has exceeded 0.72% (14.4x burn rate against 99.95% SLO) in both the 1h and 5m windows. At this rate, the monthly error budget (0.05%) will be exhausted in ~3 days. Check for etcd member failures, network partitions, or resource exhaustion (Unavailable/DeadlineExceeded gRPC codes).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd gRPC error rate critically high (>0.72%, fast burn)'
          title: 'etcd gRPC error rate critically high (>0.72%, fast burn) namespace:{{ $labels.namespace }} cluster:{{ $labels.cluster }}'
        }
        expression: '(sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_code!="OK",grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[1h])) / sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[1h])) > 0.0072) and (sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_code!="OK",grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[5m])) / sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[5m])) > 0.0072)'
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
        alert: 'userJourneyEtcdErrors6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'etcd-grpc-errors'
        }
        annotations: {
          correlationId: 'userJourneyEtcdErrors6h30m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd gRPC error rate has exceeded 0.3% (6x burn rate against 99.95% SLO) in both the 6h and 30m windows. At this rate, the monthly error budget (0.05%) will be exhausted in ~5 days. Investigate etcd cluster health, member connectivity, and recent configuration or workload changes.'
          info: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd gRPC error rate has exceeded 0.3% (6x burn rate against 99.95% SLO) in both the 6h and 30m windows. At this rate, the monthly error budget (0.05%) will be exhausted in ~5 days. Investigate etcd cluster health, member connectivity, and recent configuration or workload changes.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd gRPC error rate elevated (>0.3%, medium burn)'
          title: 'etcd gRPC error rate elevated (>0.3%, medium burn) namespace:{{ $labels.namespace }} cluster:{{ $labels.cluster }}'
        }
        expression: '(sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_code!="OK",grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[6h])) / sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[6h])) > 0.003) and (sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_code!="OK",grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[30m])) / sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[30m])) > 0.003)'
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
        alert: 'userJourneyEtcdErrors3d'
        enabled: true
        labels: {
          long_window: '3d'
          severity: '4'
          slo: 'etcd-grpc-errors'
        }
        annotations: {
          correlationId: 'userJourneyEtcdErrors3d/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd gRPC error rate has exceeded 0.05% (1x burn rate against 99.95% SLO) over a 3-day window. At this rate, the monthly error budget will be fully exhausted by end of month. This typically indicates a low-level persistent issue such as intermittent network problems, a degraded etcd member, or slow disk affecting a subset of requests.'
          info: 'HCP {{ $labels.namespace }} on {{ $labels.cluster }}: etcd gRPC error rate has exceeded 0.05% (1x burn rate against 99.95% SLO) over a 3-day window. At this rate, the monthly error budget will be fully exhausted by end of month. This typically indicates a low-level persistent issue such as intermittent network problems, a degraded etcd member, or slow disk affecting a subset of requests.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd gRPC error rate exceeds SLO (>0.05%, slow burn)'
          title: 'etcd gRPC error rate exceeds SLO (>0.05%, slow burn) namespace:{{ $labels.namespace }} cluster:{{ $labels.cluster }}'
        }
        expression: 'sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_code!="OK",grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[3d])) / sum by (namespace, cluster) (rate(grpc_server_handling_seconds_count{grpc_service=~"etcdserverpb.*",namespace=~"ocm-.*"}[3d])) > 0.0005'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
