#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource arohcpAccessClusterSloErrorAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_access_cluster_slo_error_alerts'
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
        alert: 'userJourneyAccessClusterErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of credential operations (requestcredential/revokecredentials) are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          info: 'More than 72% of credential operations (requestcredential/revokecredentials) are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-access-cluster'
          summary: 'Credential operation error rate critically high (>72%)'
          title: 'Credential operation error rate critically high (>72%)'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.72'
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
        alert: 'userJourneyAccessClusterErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of credential operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of credential operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-access-cluster'
          summary: 'Credential operation error rate elevated (>30%) for 30+ minutes'
          title: 'Credential operation error rate elevated (>30%) for 30+ minutes'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.3'
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
        alert: 'userJourneyAccessClusterErrors3d'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          severity: '4'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of credential operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of credential operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'https://aka.ms/arohcp-runbook-access-cluster'
          summary: 'Credential operation error rate exceeds SLO target (>5%) for 6+ hours'
          title: 'Credential operation error rate exceeds SLO target (>5%) for 6+ hours'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.05'
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
        alert: 'userJourneyAccessClusterErrorsDegradation'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrorsDegradation/{{ $labels.cluster }}'
          description: 'The credential operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The credential operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'https://aka.ms/arohcp-runbook-access-cluster'
          summary: 'Credential operation failure rate exceeds 15% for 30 minutes'
          title: 'Credential operation failure rate exceeds 15% for 30 minutes'
        }
        expression: 'errors:backend_credential_operation:error_rate > 0.15'
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
        alert: 'userJourneyAccessClusterStuckOperation'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterStuckOperation/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.phase }}'
          description: 'Credential operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          info: 'Credential operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          runbook_url: 'https://aka.ms/arohcp-runbook-access-cluster'
          summary: 'Credential operation for {{ $labels.resource_id }} stuck in {{ $labels.phase }} for over 1 hour'
          title: 'Credential operation for {{ $labels.resource_id }} stuck in {{ $labels.phase }} for over 1 hour'
        }
        expression: '(((time() - backend_resource_operation_start_time_seconds{operation_type=~"requestcredential|revokecredentials",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"}) and backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"accepted|provisioning|deleting",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"} == 1) > 3600) unless on (subscription_id) internal_subscription:info'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpAccessClusterSaturationAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_access_cluster_saturation_alerts'
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
        alert: 'userJourneyAccessClusterSaturationQueueDepth'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterSaturationQueueDepth/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Credential controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Credential controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'https://aka.ms/arohcp-runbook-access-cluster'
          summary: 'Credential controller workqueue {{ $labels.name }} depth is high'
          title: 'Credential controller workqueue {{ $labels.name }} depth is high'
        }
        expression: 'max by (name, cluster) (max without (prometheus_replica) (workqueue_depth{name=~".*(RequestCredential|RevokeCredentials).*",namespace="aro-hcp"})) > 10'
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
        alert: 'userJourneyAccessClusterSaturationRetryHotLoop'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterSaturationRetryHotLoop/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Credential controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Credential controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'https://aka.ms/arohcp-runbook-access-cluster'
          summary: 'Credential controller workqueue {{ $labels.name }} retry hot loop'
          title: 'Credential controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '(sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_retries_total{name=~".*(RequestCredential|RevokeCredentials).*",namespace="aro-hcp"}[10m]))) / sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_adds_total{name=~".*(RequestCredential|RevokeCredentials).*",namespace="aro-hcp"}[10m])))) > 0.5 and sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_adds_total{name=~".*(RequestCredential|RevokeCredentials).*",namespace="aro-hcp"}[10m]))) > 0.008'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpClusterProvisionSloErrorAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cluster_provision_slo_error_alerts'
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
        alert: 'userJourneyClusterProvisionErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'userJourneyClusterProvisionErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of cluster create (install) operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours. A regional install failure of this magnitude typically points at a shared dependency (e.g. registry, DNS, or ARM) rather than individual clusters.'
          info: 'More than 72% of cluster create (install) operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours. A regional install failure of this magnitude typically points at a shared dependency (e.g. registry, DNS, or ARM) rather than individual clusters.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: 'Cluster provisioning error rate critically high (>72%)'
          title: 'Cluster provisioning error rate critically high (>72%)'
        }
        expression: 'errors:backend_cluster_provision:error_rate > 0.72'
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
        alert: 'userJourneyClusterProvisionErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'userJourneyClusterProvisionErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of cluster create (install) operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of cluster create (install) operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: 'Cluster provisioning error rate elevated (>30%) for 30+ minutes'
          title: 'Cluster provisioning error rate elevated (>30%) for 30+ minutes'
        }
        expression: 'errors:backend_cluster_provision:error_rate > 0.3'
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
        alert: 'userJourneyClusterProvisionErrors3d'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          severity: '4'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'userJourneyClusterProvisionErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of cluster create (install) operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of cluster create (install) operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: 'Cluster provisioning error rate exceeds SLO target (>5%) for 6+ hours'
          title: 'Cluster provisioning error rate exceeds SLO target (>5%) for 6+ hours'
        }
        expression: 'errors:backend_cluster_provision:error_rate > 0.05'
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
        alert: 'userJourneyClusterProvisionErrorsDegradation'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'userJourneyClusterProvisionErrorsDegradation/{{ $labels.cluster }}'
          description: 'The cluster create (install) failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The cluster create (install) failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: 'Cluster provisioning failure rate exceeds 15% for 30 minutes'
          title: 'Cluster provisioning failure rate exceeds 15% for 30 minutes'
        }
        expression: 'errors:backend_cluster_provision:error_rate > 0.15'
        for: 'PT30M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource rpUserJourneyClusterUpgradeMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-user-journey-cluster-upgrade-monitor-rules'
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
        alert: 'userJourneyClusterUpgradeStuckInDesired'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-upgrade-reach-partial'
        }
        annotations: {
          correlationId: 'userJourneyClusterUpgradeStuckInDesired/{{ $labels.cluster }}{{ $labels.resource_id }}/{{ $labels.version }}'
          description: '''Cluster upgrade target version {{ $labels.version }} has been in desired for over 20 minutes without reaching partial. With the alert pending for 5 minutes, paging starts after ~25 minutes. Partial is when the target version becomes active (recognized as active) on the HCP cluster; the upgrade is in progress but not yet complete. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''Cluster upgrade target version {{ $labels.version }} has been in desired for over 20 minutes without reaching partial. With the alert pending for 5 minutes, paging starts after ~25 minutes. Partial is when the target version becomes active (recognized as active) on the HCP cluster; the upgrade is in progress but not yet complete. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-upgrade'
          summary: '{{ $labels.cluster }}: Cluster upgrade on {{ $labels.resource_id }} to version {{ $labels.version }} not progressing'
          title: '{{ $labels.cluster }}: Cluster upgrade on {{ $labels.resource_id }} to version {{ $labels.version }} not progressing'
        }
        expression: 'hosted_control_plane_upgrade:duration_in_desired:seconds > 1200'
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
        alert: 'userJourneyClusterUpgradeStuckInProgress'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-upgrade-complete'
        }
        annotations: {
          correlationId: 'userJourneyClusterUpgradeStuckInProgress/{{ $labels.cluster }}{{ $labels.resource_id }}/{{ $labels.version }}'
          description: '''Cluster upgrade target version {{ $labels.version }} has been in progress for over 30 minutes without reaching completed. With the alert pending for 5 minutes, paging starts after ~35 minutes. Completed is when the target version upgrade has finished and is reported as complete on the HCP cluster. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          info: '''Cluster upgrade target version {{ $labels.version }} has been in progress for over 30 minutes without reaching completed. With the alert pending for 5 minutes, paging starts after ~35 minutes. Completed is when the target version upgrade has finished and is reported as complete on the HCP cluster. Investigate backend_cluster_version_info on the affected cluster.
Service Cluster: {{ $labels.cluster }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-upgrade'
          summary: '{{ $labels.cluster }}: Cluster upgrade on {{ $labels.resource_id }} to version {{ $labels.version }} stuck'
          title: '{{ $labels.cluster }}: Cluster upgrade on {{ $labels.resource_id }} to version {{ $labels.version }} stuck'
        }
        expression: 'hosted_control_plane_upgrade:duration_in_progress:seconds > 1800'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpNodepoolSloErrorAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_nodepool_slo_error_alerts'
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
        alert: 'userJourneyNodePoolErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          severity: 'info'
          short_window: '5m'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'userJourneyNodePoolErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          info: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: 'Node Pool operation error rate critically high (>72%)'
          title: 'Node Pool operation error rate critically high (>72%)'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.72'
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
        alert: 'userJourneyNodePoolErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: 'info'
          short_window: '30m'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'userJourneyNodePoolErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: 'Node Pool operation error rate elevated (>30%) for 30+ minutes'
          title: 'Node Pool operation error rate elevated (>30%) for 30+ minutes'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.3'
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
        alert: 'userJourneyNodePoolErrors3d'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          severity: 'info'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'userJourneyNodePoolErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: 'Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
          title: 'Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.05'
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
        alert: 'userJourneyNodePoolErrorsDegradation'
        enabled: true
        labels: {
          component: 'slo'
          severity: 'info'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'userJourneyNodePoolErrorsDegradation/{{ $labels.cluster }}'
          description: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: 'Node Pool operation failure rate exceeds 15% for 30 minutes'
          title: 'Node Pool operation failure rate exceeds 15% for 30 minutes'
        }
        expression: 'errors:backend_nodepool_operation:error_rate > 0.15'
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
        alert: 'userJourneyNodePoolStuckOperation'
        enabled: true
        labels: {
          component: 'slo'
          severity: 'info'
        }
        annotations: {
          correlationId: 'userJourneyNodePoolStuckOperation/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.phase }}'
          description: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 2 hours. Stuck operations are invisible to success/failure SLIs and require investigation.'
          info: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 2 hours. Stuck operations are invisible to success/failure SLIs and require investigation.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: 'Node Pool operation for {{ $labels.resource_id }} stuck in {{ $labels.phase }} for over 2 hours'
          title: 'Node Pool operation for {{ $labels.resource_id }} stuck in {{ $labels.phase }} for over 2 hours'
        }
        expression: '(max by (cluster, environment, region, subscription_id, resource_id, resource_type, operation_type, phase) (max_over_time((((time() - backend_resource_operation_start_time_seconds{resource_type=~".*nodepools"}) and backend_resource_operation_phase_info{phase=~"updating|deleting",resource_type=~".*nodepools"} == 1) > 7200)[6h:5m]))) unless on (subscription_id) internal_subscription:info'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpNodepoolSaturationAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_nodepool_saturation_alerts'
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
        alert: 'userJourneyNodePoolSaturationQueueDepth'
        enabled: true
        labels: {
          component: 'slo'
          severity: 'info'
        }
        annotations: {
          correlationId: 'userJourneyNodePoolSaturationQueueDepth/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: 'Node Pool controller workqueue {{ $labels.name }} depth is high'
          title: 'Node Pool controller workqueue {{ $labels.name }} depth is high'
        }
        expression: 'max by (name, cluster) (max without (prometheus_replica) (workqueue_depth{name=~".*NodePool.*",namespace="aro-hcp"})) > 10'
        for: 'PT5M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
