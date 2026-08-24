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
        alert: 'MgmtAgentCapacityReportingSyncFailing'
        enabled: true
        labels: {
          component: 'capacity'
          severity: 'warning'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'MgmtAgentCapacityReportingSyncFailing/{{ $labels.cluster }}'
          description: 'Capacity reporting on management cluster {{ $labels.cluster }} has been failing continuously for at least 5 minutes. The CapacityReport CR may contain stale data, which can affect fleet-level placement decisions. Check mgmt-agent logs for root cause.'
          info: 'Capacity reporting on management cluster {{ $labels.cluster }} has been failing continuously for at least 5 minutes. The CapacityReport CR may contain stale data, which can affect fleet-level placement decisions. Check mgmt-agent logs for root cause.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-runbook/mgmt-cluster-capacity'
          summary: 'Capacity reporting sync failing on {{ $labels.cluster }}'
          title: 'Capacity reporting sync failing on {{ $labels.cluster }}'
        }
        expression: 'increase(capacity_reporting_sync_errors_total[5m]) >= 8'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource veleroBackupAlertRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'velero-backup-alert-rules'
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
        alert: 'VeleroHourlyBackupStuckWarning'
        enabled: true
        labels: {
          component: 'velero'
          severity: '3'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'VeleroHourlyBackupStuckWarning/{{ $labels.cluster }}/{{ $labels.hosted_cluster }}'
          description: 'No successful Velero hourly backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 2 hours (last success {{ $value | humanizeDuration }} ago). Hourly cadence is 1h; this is the primary restore-point freshness signal.'
          info: 'No successful Velero hourly backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 2 hours (last success {{ $value | humanizeDuration }} ago). Hourly cadence is 1h; this is the primary restore-point freshness signal.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-tsg-velerobackup'
          summary: 'Velero hourly backup stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
          title: 'Velero hourly backup stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
        }
        expression: '(time() - max by (cluster, hosted_cluster) (max without (prometheus_replica) (label_replace(velero_backup_last_successful_timestamp{schedule=~".+-hourly"}, "hosted_cluster", "$1", "schedule", "(.+)-hourly")))) > 2 * 3600'
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
        alert: 'VeleroHourlyBackupStuckCritical'
        enabled: true
        labels: {
          component: 'velero'
          severity: '3'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'VeleroHourlyBackupStuckCritical/{{ $labels.cluster }}/{{ $labels.hosted_cluster }}'
          description: 'No successful Velero hourly backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 6 hours (last success {{ $value | humanizeDuration }} ago). HCP has no recent restore point; immediate investigation required.'
          info: 'No successful Velero hourly backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 6 hours (last success {{ $value | humanizeDuration }} ago). HCP has no recent restore point; immediate investigation required.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-tsg-velerobackup'
          summary: 'Velero hourly backup critically stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
          title: 'Velero hourly backup critically stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
        }
        expression: '(time() - max by (cluster, hosted_cluster) (max without (prometheus_replica) (label_replace(velero_backup_last_successful_timestamp{schedule=~".+-hourly"}, "hosted_cluster", "$1", "schedule", "(.+)-hourly")))) > 6 * 3600'
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
        alert: 'VeleroDailyBackupStuck'
        enabled: true
        labels: {
          component: 'velero'
          severity: '4'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'VeleroDailyBackupStuck/{{ $labels.cluster }}/{{ $labels.hosted_cluster }}'
          description: 'No successful Velero daily backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 24 hours (last success {{ $value | humanizeDuration }} ago). Daily cadence is 24h. Restore is not necessarily at risk if hourly backups are healthy; this affects 30-day retention/compliance coverage.'
          info: 'No successful Velero daily backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 24 hours (last success {{ $value | humanizeDuration }} ago). Daily cadence is 24h. Restore is not necessarily at risk if hourly backups are healthy; this affects 30-day retention/compliance coverage.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-tsg-velerobackup'
          summary: 'Velero daily backup stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
          title: 'Velero daily backup stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
        }
        expression: '(time() - max by (cluster, hosted_cluster) (max without (prometheus_replica) (label_replace(velero_backup_last_successful_timestamp{schedule=~".+-daily"}, "hosted_cluster", "$1", "schedule", "(.+)-daily")))) > 24 * 3600'
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
        alert: 'VeleroWeeklyBackupStuck'
        enabled: true
        labels: {
          component: 'velero'
          severity: '4'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'VeleroWeeklyBackupStuck/{{ $labels.cluster }}/{{ $labels.hosted_cluster }}'
          description: 'No successful Velero weekly backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 168 hours (last success {{ $value | humanizeDuration }} ago). Weekly cadence is 7d. Restore is not necessarily at risk if hourly backups are healthy; this affects 90-day retention/compliance coverage.'
          info: 'No successful Velero weekly backup for HostedCluster {{ $labels.hosted_cluster }} on management cluster {{ $labels.cluster }} in over 168 hours (last success {{ $value | humanizeDuration }} ago). Weekly cadence is 7d. Restore is not necessarily at risk if hourly backups are healthy; this affects 90-day retention/compliance coverage.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-tsg-velerobackup'
          summary: 'Velero weekly backup stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
          title: 'Velero weekly backup stuck for HostedCluster {{ $labels.hosted_cluster }} on {{ $labels.cluster }}'
        }
        expression: '(time() - max by (cluster, hosted_cluster) (max without (prometheus_replica) (label_replace(velero_backup_last_successful_timestamp{schedule=~".+-weekly"}, "hosted_cluster", "$1", "schedule", "(.+)-weekly")))) > 168 * 3600'
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
        alert: 'VeleroCSISnapshotFailureRateHigh'
        enabled: true
        labels: {
          component: 'velero'
          severity: '3'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'VeleroCSISnapshotFailureRateHigh/{{ $labels.cluster }}'
          description: '{{ $value | humanizePercentage }} of Velero CSI snapshot attempts failed on management cluster {{ $labels.cluster }} over the last hour (threshold 10%). Volume snapshots backing HCP backups may be failing.'
          info: '{{ $value | humanizePercentage }} of Velero CSI snapshot attempts failed on management cluster {{ $labels.cluster }} over the last hour (threshold 10%). Volume snapshots backing HCP backups may be failing.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-tsg-velerobackup'
          summary: 'Elevated Velero CSI snapshot failure rate on {{ $labels.cluster }}'
          title: 'Elevated Velero CSI snapshot failure rate on {{ $labels.cluster }}'
        }
        expression: 'velero:csi_snapshot:failure_ratio1h > 0.1 and velero:csi_snapshot_attempt:increase1h >= 2'
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
        alert: 'VeleroBackupDeletionFailureRateHigh'
        enabled: true
        labels: {
          component: 'velero'
          severity: '3'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'VeleroBackupDeletionFailureRateHigh/{{ $labels.cluster }}'
          description: '{{ $value | humanizePercentage }} of Velero backup deletion attempts failed on management cluster {{ $labels.cluster }} over the last hour (threshold 10%). Expired backups may not be reclaimed.'
          info: '{{ $value | humanizePercentage }} of Velero backup deletion attempts failed on management cluster {{ $labels.cluster }} over the last hour (threshold 10%). Expired backups may not be reclaimed.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-tsg-velerobackup'
          summary: 'Elevated Velero backup deletion failure rate on {{ $labels.cluster }}'
          title: 'Elevated Velero backup deletion failure rate on {{ $labels.cluster }}'
        }
        expression: 'velero:backup_deletion:failure_ratio1h > 0.1 and velero:backup_deletion_attempt:increase1h >= 2'
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
        alert: 'VeleroMetricsAbsent'
        enabled: true
        labels: {
          component: 'velero'
          severity: '3'
          team: 'hcp-sl'
        }
        annotations: {
          correlationId: 'VeleroMetricsAbsent/{{ $labels.cluster }}'
          description: 'Velero backup metrics have been absent on management cluster {{ $labels.cluster }} for at least 15 minutes. Velero may be down or its PodMonitor scrape is broken; all other Velero backup alerts are blind on this cluster until this is resolved.'
          info: 'Velero backup metrics have been absent on management cluster {{ $labels.cluster }} for at least 15 minutes. Velero may be down or its PodMonitor scrape is broken; all other Velero backup alerts are blind on this cluster until this is resolved.'
          owning_team: 'hcp-sl'
          runbook_url: 'https://aka.ms/arohcp-tsg-velerobackup'
          summary: 'Velero metrics absent on {{ $labels.cluster }}'
          title: 'Velero metrics absent on {{ $labels.cluster }}'
        }
        expression: 'group by (cluster) (underlay_clusters{cluster!~".*-svc-.*"}) unless on (cluster) group by (cluster) (velero_backup_attempt_total)'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
