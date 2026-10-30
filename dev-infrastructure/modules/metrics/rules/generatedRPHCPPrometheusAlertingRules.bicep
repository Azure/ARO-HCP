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

resource arohcpBackupSloAvailabilityAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_backup_slo_availability_alerts'
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
        alert: 'userJourneyBackupAvailability6Hourly'
        enabled: true
        labels: {
          cadence: '6-hourly'
          component: 'slo'
          severity: '3'
          slo: 'backup-availability'
        }
        annotations: {
          correlationId: 'userJourneyBackupAvailability6Hourly/{{ $labels.cluster }}'
          description: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has had no successful Velero 6-hourly backup in over 13 hours (2x the 6h cadence + 1h grace). This is the primary restore-point freshness signal. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s). Known gap: does not yet exclude an individually-paused schedule - check pause state via the admin API before paging further (ARO-29949).'
          info: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has had no successful Velero 6-hourly backup in over 13 hours (2x the 6h cadence + 1h grace). This is the primary restore-point freshness signal. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s). Known gap: does not yet exclude an individually-paused schedule - check pause state via the admin API before paging further (ARO-29949).'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'at least one HostedCluster has no successful 6-hourly backup in over 13h on {{ $labels.cluster }}'
          title: 'at least one HostedCluster has no successful 6-hourly backup in over 13h on {{ $labels.cluster }}'
        }
        expression: '(time() - min by (cluster, region) (max by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-6-hourly"} and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)))) > 13 * 3600'
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
        alert: 'userJourneyBackupAvailabilityDaily'
        enabled: true
        labels: {
          cadence: 'daily'
          component: 'slo'
          severity: '3'
          slo: 'backup-availability'
        }
        annotations: {
          correlationId: 'userJourneyBackupAvailabilityDaily/{{ $labels.cluster }}'
          description: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has had no successful Velero daily backup in over 49 hours (2x the 24h cadence + 1h grace). Daily backups carry longer retention than 6-hourly; a gap here is a separate restore-point loss even if 6-hourly is healthy. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s). Known gap: does not yet exclude an individually-paused schedule - check pause state via the admin API before paging further (ARO-29949).'
          info: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has had no successful Velero daily backup in over 49 hours (2x the 24h cadence + 1h grace). Daily backups carry longer retention than 6-hourly; a gap here is a separate restore-point loss even if 6-hourly is healthy. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s). Known gap: does not yet exclude an individually-paused schedule - check pause state via the admin API before paging further (ARO-29949).'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'at least one HostedCluster has no successful daily backup in over 49h on {{ $labels.cluster }}'
          title: 'at least one HostedCluster has no successful daily backup in over 49h on {{ $labels.cluster }}'
        }
        expression: '(time() - min by (cluster, region) (max by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-daily"} and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)))) > 49 * 3600'
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
        alert: 'userJourneyBackupAvailabilityWeekly'
        enabled: true
        labels: {
          cadence: 'weekly'
          component: 'slo'
          severity: '3'
          slo: 'backup-availability'
        }
        annotations: {
          correlationId: 'userJourneyBackupAvailabilityWeekly/{{ $labels.cluster }}'
          description: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has had no successful Velero weekly backup in over 337 hours (2x the 7d cadence + 1h grace). Weekly backups carry the longest retention; a gap here is a separate restore-point loss even if 6-hourly/daily are healthy. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s). Known gap: does not yet exclude an individually-paused schedule - check pause state via the admin API before paging further (ARO-29949).'
          info: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has had no successful Velero weekly backup in over 337 hours (2x the 7d cadence + 1h grace). Weekly backups carry the longest retention; a gap here is a separate restore-point loss even if 6-hourly/daily are healthy. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s). Known gap: does not yet exclude an individually-paused schedule - check pause state via the admin API before paging further (ARO-29949).'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'at least one HostedCluster has no successful weekly backup in over 337h on {{ $labels.cluster }}'
          title: 'at least one HostedCluster has no successful weekly backup in over 337h on {{ $labels.cluster }}'
        }
        expression: '(time() - min by (cluster, region) (max by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-weekly"} and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)))) > 337 * 3600'
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
        alert: 'userJourneyBackupNeverSucceeded6Hourly'
        enabled: true
        labels: {
          cadence: '6-hourly'
          component: 'slo'
          severity: '3'
          slo: 'backup-availability'
        }
        annotations: {
          correlationId: 'userJourneyBackupNeverSucceeded6Hourly/{{ $labels.cluster }}'
          description: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has reported a 6-hourly schedule status without a successful backup timestamp for 12h. Check initial backup failures, metric resets, and schedule pause state. This signal requires an observed schedule status; it cannot detect a schedule that was never delivered. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s).'
          info: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has reported a 6-hourly schedule status without a successful backup timestamp for 12h. Check initial backup failures, metric resets, and schedule pause state. This signal requires an observed schedule status; it cannot detect a schedule that was never delivered. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s).'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'at least one HostedCluster has no successful backup timestamp after 12h of observed 6-hourly schedule status on {{ $labels.cluster }}'
          title: 'at least one HostedCluster has no successful backup timestamp after 12h of observed 6-hourly schedule status on {{ $labels.cluster }}'
        }
        expression: 'count by (cluster, region) (((group by (cluster, schedule, namespace, region) (velero_hcp_backup_last_status{schedule=~".+-6-hourly"}) unless on (cluster, schedule) group by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-6-hourly"})) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) and on (cluster, schedule) (count_over_time((group by (cluster, schedule, region) (velero_hcp_backup_last_status{schedule=~".+-6-hourly"}) unless on (cluster, schedule) group by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-6-hourly"}))[12h:1m]) >= 716)) > 0'
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
        alert: 'userJourneyBackupNeverSucceededDaily'
        enabled: true
        labels: {
          cadence: 'daily'
          component: 'slo'
          severity: '3'
          slo: 'backup-availability'
        }
        annotations: {
          correlationId: 'userJourneyBackupNeverSucceededDaily/{{ $labels.cluster }}'
          description: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has reported a daily schedule status without a successful backup timestamp for 48h. Check initial backup failures, metric resets, and schedule pause state. This signal requires an observed schedule status; it cannot detect a schedule that was never delivered. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s).'
          info: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has reported a daily schedule status without a successful backup timestamp for 48h. Check initial backup failures, metric resets, and schedule pause state. This signal requires an observed schedule status; it cannot detect a schedule that was never delivered. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s).'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'at least one HostedCluster has no successful backup timestamp after 48h of observed daily schedule status on {{ $labels.cluster }}'
          title: 'at least one HostedCluster has no successful backup timestamp after 48h of observed daily schedule status on {{ $labels.cluster }}'
        }
        expression: 'count by (cluster, region) (((group by (cluster, schedule, namespace, region) (velero_hcp_backup_last_status{schedule=~".+-daily"}) unless on (cluster, schedule) group by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-daily"})) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) and on (cluster, schedule) (count_over_time((group by (cluster, schedule, region) (velero_hcp_backup_last_status{schedule=~".+-daily"}) unless on (cluster, schedule) group by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-daily"}))[2d:5m]) >= 574)) > 0'
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
        alert: 'userJourneyBackupNeverSucceededWeekly'
        enabled: true
        labels: {
          cadence: 'weekly'
          component: 'slo'
          severity: '3'
          slo: 'backup-availability'
        }
        annotations: {
          correlationId: 'userJourneyBackupNeverSucceededWeekly/{{ $labels.cluster }}'
          description: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has reported a weekly schedule status without a successful backup timestamp for 336h. Check initial backup failures, metric resets, and schedule pause state. This signal requires an observed schedule status; it cannot detect a schedule that was never delivered. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s).'
          info: 'At least one HostedCluster namespace on management cluster {{ $labels.cluster }} has reported a weekly schedule status without a successful backup timestamp for 336h. Check initial backup failures, metric resets, and schedule pause state. This signal requires an observed schedule status; it cannot detect a schedule that was never delivered. One alert fires per management cluster regardless of how many HostedClusters are affected - use the Velero dashboard\'s "Hours Since Last Backup" and "Backup Schedule Last Status" panels to identify which schedule(s).'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'at least one HostedCluster has no successful backup timestamp after 336h of observed weekly schedule status on {{ $labels.cluster }}'
          title: 'at least one HostedCluster has no successful backup timestamp after 336h of observed weekly schedule status on {{ $labels.cluster }}'
        }
        expression: 'count by (cluster, region) (((group by (cluster, schedule, namespace, region) (velero_hcp_backup_last_status{schedule=~".+-weekly"}) unless on (cluster, schedule) group by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-weekly"})) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) and on (cluster, schedule) (count_over_time((group by (cluster, schedule, region) (velero_hcp_backup_last_status{schedule=~".+-weekly"}) unless on (cluster, schedule) group by (cluster, schedule, region) (velero_hcp_backup_last_successful_timestamp{schedule=~".+-weekly"}))[2w:15m]) >= 1341)) > 0'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpBackupSloErrorAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_backup_slo_error_alerts'
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
        alert: 'userJourneyBackupErrors'
        enabled: true
        labels: {
          component: 'slo'
          severity: '3'
          slo: 'backup-errors'
        }
        annotations: {
          correlationId: 'userJourneyBackupErrors/{{ $labels.cluster }}'
          description: 'Velero backup failed-attempt share on management cluster {{ $labels.cluster }} is above the 0.5% SLO boundary (99.5% success budget), sustained over a 6h window for 30m.'
          info: 'Velero backup failed-attempt share on management cluster {{ $labels.cluster }} is above the 0.5% SLO boundary (99.5% success budget), sustained over a 6h window for 30m.'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'Velero backup failure rate above SLO (>0.5%) on {{ $labels.cluster }}'
          title: 'Velero backup failure rate above SLO (>0.5%) on {{ $labels.cluster }}'
        }
        expression: '((sum by (cluster, region) (rate(velero_hcp_backup_success_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) or 0 * sum by (cluster, region) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]))) / ((sum by (cluster, region) (rate(velero_hcp_backup_success_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) or 0 * sum by (cluster, region) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]))) + (sum by (cluster, region) (rate(velero_hcp_backup_failure_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) or 0 * sum by (cluster, region) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]))) + (sum by (cluster, region) (rate(velero_hcp_backup_partial_failure_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) or 0 * sum by (cluster, region) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]))) + (sum by (cluster, region) (rate(velero_hcp_backup_validation_failure_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0)) or 0 * sum by (cluster, region) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]))))) < 0.995 and on (cluster) sum by (cluster, region) (max without (prometheus_replica) (increase(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0))) >= 5'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpBackupSloLatencyAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_backup_slo_latency_alerts'
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
        alert: 'userJourneyBackupLatency'
        enabled: true
        labels: {
          component: 'slo'
          severity: '3'
          slo: 'backup-latency'
        }
        annotations: {
          correlationId: 'userJourneyBackupLatency/{{ $labels.cluster }}'
          description: 'Velero backup p99 duration on management cluster {{ $labels.cluster }} is above 20 minutes over a 6h window, sustained for 15m (SLO: p99 < 20m, inside the 10m CSI snapshot / 15m item-operation timeouts). Same recurring-cadence scope and >=5 completed-backup volume floor as userJourneyBackupErrors, so a single slow backup can\'t swing the p99 on its own.'
          info: 'Velero backup p99 duration on management cluster {{ $labels.cluster }} is above 20 minutes over a 6h window, sustained for 15m (SLO: p99 < 20m, inside the 10m CSI snapshot / 15m item-operation timeouts). Same recurring-cadence scope and >=5 completed-backup volume floor as userJourneyBackupErrors, so a single slow backup can\'t swing the p99 on its own.'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'Velero backup p99 duration above SLO (>20m) on {{ $labels.cluster }}'
          title: 'Velero backup p99 duration above SLO (>20m) on {{ $labels.cluster }}'
        }
        expression: '(histogram_quantile(0.99, sum by (cluster, le, region) (rate(velero_hcp_backup_duration_seconds_bucket{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0))) > 1200) and on (cluster) sum by (cluster, region) (max without (prometheus_replica) (increase(velero_hcp_backup_duration_seconds_count{schedule=~".+-(6-hourly|daily|weekly)"}[6h]) and on (cluster, namespace) max by (cluster, namespace, region) (hostedClusterAPI_deletion_timestamp_seconds == 0))) >= 5'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpBackupSloTrafficAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_backup_slo_traffic_alerts'
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
        alert: 'userJourneyBackupTraffic'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'backup-traffic'
        }
        annotations: {
          correlationId: 'userJourneyBackupTraffic/{{ $labels.cluster }}'
          description: 'Velero backup attempt rate on management cluster {{ $labels.cluster }} is below 10% of its 1-day average, sustained across a 12h window for 1h. Likely a global/per-cluster backup-schedule pause (check backend.backupScheduleState and per-cluster admin API state before paging further), the Backup Schedule Controller stuck, or kube-applier delivery broken - not an individual HC\'s problem.'
          info: 'Velero backup attempt rate on management cluster {{ $labels.cluster }} is below 10% of its 1-day average, sustained across a 12h window for 1h. Likely a global/per-cluster backup-schedule pause (check backend.backupScheduleState and per-cluster admin API state before paging further), the Backup Schedule Controller stuck, or kube-applier delivery broken - not an individual HC\'s problem.'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'Velero backup attempts collapsed below 10% of 1d baseline on {{ $labels.cluster }}'
          title: 'Velero backup attempts collapsed below 10% of 1d baseline on {{ $labels.cluster }}'
        }
        expression: '(avg_over_time((sum by (cluster, region) (max without (prometheus_replica) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[5m]))))[12h:1m]) < 0.1 * avg_over_time((sum by (cluster, region) (max without (prometheus_replica) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[5m]))))[1d:1m])) and avg_over_time((sum by (cluster, region) (max without (prometheus_replica) (rate(velero_hcp_backup_attempt_total{schedule=~".+-(6-hourly|daily|weekly)"}[5m]))))[1d:1m]) > 0'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpBackupSloGateHealthAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_backup_slo_gate_health_alerts'
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
        alert: 'userJourneyBackupDeletionGaugeAbsent'
        enabled: true
        labels: {
          component: 'slo'
          severity: '3'
          slo: 'backup-availability'
        }
        annotations: {
          correlationId: 'userJourneyBackupDeletionGaugeAbsent/{{ $labels.cluster }}'
          description: 'Management cluster {{ $labels.cluster }} exports velero backup-schedule metrics but no hostedClusterAPI_deletion_timestamp_seconds series. The backup SLO alerts gate on that gauge and fail closed, so they are currently blind on this cluster. Check kube-state-metrics health and the metric\'s relabeling/routing to the HCP workspace. Until this clears, backup failures on this cluster will not alert.'
          info: 'Management cluster {{ $labels.cluster }} exports velero backup-schedule metrics but no hostedClusterAPI_deletion_timestamp_seconds series. The backup SLO alerts gate on that gauge and fail closed, so they are currently blind on this cluster. Check kube-state-metrics health and the metric\'s relabeling/routing to the HCP workspace. Until this clears, backup failures on this cluster will not alert.'
          runbook_url: 'https://aka.ms/arohcp-runbook-backup'
          summary: 'deletion-timestamp gauge missing while velero backups are reported on {{ $labels.cluster }}'
          title: 'deletion-timestamp gauge missing while velero backups are reported on {{ $labels.cluster }}'
        }
        expression: 'group by (cluster, region) (velero_hcp_backup_last_status{schedule=~".+-(6-hourly|daily|weekly)"}) unless on (cluster) group by (cluster, region) (hostedClusterAPI_deletion_timestamp_seconds)'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
