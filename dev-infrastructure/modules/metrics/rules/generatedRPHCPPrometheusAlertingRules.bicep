#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource hcpEtcdAvailabilityAlertRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-etcd-availability-alert-rules'
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
          severity: '4'
          short_window: '5m'
          slo: 'etcd-backend-commit-duration'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Fast burn (1h/5m).'
          info: '{{ $labels.namespace }} etcd has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd backend commit duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd backend commit duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_backend_commit_duration_seconds_bucket:rate5m) > 0.1 and histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_backend_commit_duration_seconds_bucket:rate1h) > 0.1) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
        for: 'PT5M'
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
        alert: 'userJourneyEtcdBackendCommitDurationHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '4'
          short_window: '30m'
          slo: 'etcd-backend-commit-duration'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Medium burn (6h/30m).'
          info: '{{ $labels.namespace }} etcd has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd backend commit duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd backend commit duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_backend_commit_duration_seconds_bucket:rate30m) > 0.1 and histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_backend_commit_duration_seconds_bucket:rate6h) > 0.1) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdBackendCommitDurationHigh3d6h'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
          slo: 'etcd-backend-commit-duration'
        }
        annotations: {
          correlationId: 'userJourneyEtcdBackendCommitDurationHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Slow burn (3d/6h).'
          info: '{{ $labels.namespace }} etcd has 99th percentile backend commit duration of {{ $value }}s (threshold: 100ms). Slow disk performance may impact write performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Slow burn (3d/6h).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd backend commit duration is chronically high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd backend commit duration is chronically high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_backend_commit_duration_seconds_bucket:rate6h) > 0.1 and histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_backend_commit_duration_seconds_bucket:rate3d) > 0.1) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdDatabaseHighFragmentation'
        enabled: true
        labels: {
          component: 'hcp'
          severity: '4'
          slo: 'etcd-database-fragmentation'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseHighFragmentation/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          info: '{{ $labels.namespace }} etcd database in-use ratio is {{ $value | humanizePercentage }} (threshold: 25%). Database defragmentation may be needed to reclaim space. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database fragmentation is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd database fragmentation is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((max by (namespace, cluster, pod, region) (etcd_mvcc_db_total_size_in_use_in_bytes) / max by (namespace, cluster, pod, region) (etcd_mvcc_db_total_size_in_bytes) < 0.25) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdDatabaseSizeExceeded'
        enabled: true
        labels: {
          component: 'hcp'
          severity: '4'
          slo: 'etcd-database-size'
        }
        annotations: {
          correlationId: 'userJourneyEtcdDatabaseSizeExceeded/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd database size is {{ $value | humanizePercentage }} of quota (threshold: 85%). Database may need compaction or quota increase. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          info: '{{ $labels.namespace }} etcd database size is {{ $value | humanizePercentage }} of quota (threshold: 85%). Database may need compaction or quota increase. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd database size is too large for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd database size is too large for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((max without (prometheus_replica) (etcd_mvcc_db_total_size_in_bytes / etcd_server_quota_backend_bytes) > 0.85) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdFrequentLeaderChanges'
        enabled: true
        labels: {
          component: 'hcp'
          severity: '4'
          slo: 'etcd-leader-changes'
        }
        annotations: {
          correlationId: 'userJourneyEtcdFrequentLeaderChanges/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          info: '{{ $labels.namespace }} etcd has seen {{ $value }} leader changes in the last 15 minutes (threshold: 3). This may indicate network issues or cluster instability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd cluster experiencing frequent leader changes for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd cluster experiencing frequent leader changes for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((max by (namespace, cluster, region) (increase(etcd_server_leader_changes_seen_total[15m])) > 3) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
        for: 'PT5M'
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
        alert: 'userJourneyEtcdNoLeader'
        enabled: true
        labels: {
          component: 'hcp'
          severity: '4'
          slo: 'etcd-has-leader'
        }
        annotations: {
          correlationId: 'userJourneyEtcdNoLeader/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd member has no leader. The cluster cannot process writes until a leader is elected. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          info: '{{ $labels.namespace }} etcd member has no leader. The cluster cannot process writes until a leader is elected. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd member has no leader for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd member has no leader for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((min without (prometheus_replica) (etcd_server_has_leader) == 0) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
        for: 'PT5M'
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
          severity: '4'
          short_window: '5m'
          slo: 'etcd-peer-round-trip-time'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Fast burn (1h/5m).'
          info: '{{ $labels.namespace }} etcd has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd peer round-trip time is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd peer round-trip time is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster:etcd_network_peer_round_trip_time_seconds_bucket:rate5m) > 0.1 and histogram_quantile(0.99, namespace_cluster:etcd_network_peer_round_trip_time_seconds_bucket:rate1h) > 0.1) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
        for: 'PT5M'
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
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '4'
          short_window: '30m'
          slo: 'etcd-peer-round-trip-time'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Medium burn (6h/30m).'
          info: '{{ $labels.namespace }} etcd has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd peer round-trip time is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd peer round-trip time is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster:etcd_network_peer_round_trip_time_seconds_bucket:rate30m) > 0.1 and histogram_quantile(0.99, namespace_cluster:etcd_network_peer_round_trip_time_seconds_bucket:rate6h) > 0.1) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdPeerRoundTripTimeHigh3d6h'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
          slo: 'etcd-peer-round-trip-time'
        }
        annotations: {
          correlationId: 'userJourneyEtcdPeerRoundTripTimeHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Slow burn (3d/6h).'
          info: '{{ $labels.namespace }} etcd has 99th percentile peer round-trip time of {{ $value }}s (threshold: 100ms). Network latency between peers may be affecting cluster performance. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Slow burn (3d/6h).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd peer round-trip time is chronically high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd peer round-trip time is chronically high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster:etcd_network_peer_round_trip_time_seconds_bucket:rate6h) > 0.1 and histogram_quantile(0.99, namespace_cluster:etcd_network_peer_round_trip_time_seconds_bucket:rate3d) > 0.1) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdWALFsyncDurationHigh1h5m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '1h'
          severity: '4'
          short_window: '5m'
          slo: 'etcd-wal-fsync-duration'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Fast burn (1h/5m).'
          info: '{{ $labels.namespace }} etcd has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Fast burn (1h/5m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd WAL fsync duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd WAL fsync duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_wal_fsync_duration_seconds_bucket:rate5m) > 0.05 and histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_wal_fsync_duration_seconds_bucket:rate1h) > 0.05) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
        for: 'PT5M'
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
        alert: 'userJourneyEtcdWALFsyncDurationHigh6h30m'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '6h'
          severity: '4'
          short_window: '30m'
          slo: 'etcd-wal-fsync-duration'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Medium burn (6h/30m).'
          info: '{{ $labels.namespace }} etcd has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Medium burn (6h/30m).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd WAL fsync duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd WAL fsync duration is high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_wal_fsync_duration_seconds_bucket:rate30m) > 0.05 and histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_wal_fsync_duration_seconds_bucket:rate6h) > 0.05) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdWALFsyncDurationHigh3d6h'
        enabled: true
        labels: {
          component: 'hcp'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
          slo: 'etcd-wal-fsync-duration'
        }
        annotations: {
          correlationId: 'userJourneyEtcdWALFsyncDurationHigh/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} etcd has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Slow burn (3d/6h).'
          info: '{{ $labels.namespace }} etcd has 99th percentile WAL fsync duration of {{ $value }}s (threshold: 50ms). Slow disk performance may impact cluster stability. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}. Slow burn (3d/6h).'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd WAL fsync duration is chronically high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd WAL fsync duration is chronically high for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_wal_fsync_duration_seconds_bucket:rate6h) > 0.05 and histogram_quantile(0.99, namespace_cluster_pod:etcd_disk_wal_fsync_duration_seconds_bucket:rate3d) > 0.05) * on (namespace, cluster) group_left (resource_id) label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)") * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
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
        alert: 'userJourneyEtcdMetricsAbsent'
        enabled: true
        labels: {
          component: 'hcp'
          severity: '4'
          slo: 'etcd-metrics-absent'
        }
        annotations: {
          correlationId: 'userJourneyEtcdMetricsAbsent/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '{{ $labels.namespace }} has not reported etcd metrics for at least 15 minutes. All etcd availability alerts are blind for this cluster. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          info: '{{ $labels.namespace }} has not reported etcd metrics for at least 15 minutes. All etcd availability alerts are blind for this cluster. Resource ID: {{ $labels.resource_id }}. Subscription ID: {{ $labels.subscription_id }}.'
          runbook_url: 'https://aka.ms/arohcp-runbook-etcd'
          summary: 'etcd metrics are absent for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
          title: 'etcd metrics are absent for {{ $labels.namespace }} ({{ $labels.resource_id }}, {{ $labels.subscription_id }})'
        }
        expression: '((max by (namespace, cluster, resource_id, region) (label_replace(info:kube_namespace:hcp_cluster_resource_id, "resource_id", "$1", "hcp_cluster_resource_id", "(.*)")) unless on (namespace, cluster) count by (namespace, cluster, region) (etcd_server_has_leader)) * on (namespace, cluster) group_left (subscription_id) label_replace(info:kube_namespace:hcp_cluster_subscription_id, "subscription_id", "$1", "hcp_cluster_subscription_id", "(.*)")) unless on (subscription_id) internal_subscription:info'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

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
          severity: '2'
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
        alert: 'userJourneyKubeApiserverAvailability6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: '2'
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
        alert: 'hypershiftCPODown'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '3'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftCPODown/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator has had no available pods for 10 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator has had no available pods for 10 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hypershift-operators.md'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator has no available pods'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator has no available pods'
        }
        expression: 'max by (cluster, namespace, region) (kube_deployment_spec_replicas{deployment="control-plane-operator",namespace=~"ocm-.*"}) > 0 and on (cluster, namespace) max by (cluster, namespace, region) (kube_deployment_status_replicas_available{deployment="control-plane-operator",namespace=~"ocm-.*"}) == 0'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
