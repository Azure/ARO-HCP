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
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of credential operations (requestcredential/revokecredentials) are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          info: 'More than 72% of credential operations (requestcredential/revokecredentials) are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation error rate critically high (>72%)'
          title: '{{ $labels.cluster }}: Credential operation error rate critically high (>72%)'
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
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of credential operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of credential operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation error rate elevated (>30%) for 30+ minutes'
          title: '{{ $labels.cluster }}: Credential operation error rate elevated (>30%) for 30+ minutes'
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
          long_window: '3d'
          severity: '4'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of credential operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of credential operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation error rate exceeds SLO target (>5%) for 6+ hours'
          title: '{{ $labels.cluster }}: Credential operation error rate exceeds SLO target (>5%) for 6+ hours'
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
          severity: '4'
          slo: 'access-cluster-errors'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterErrorsDegradation/{{ $labels.cluster }}'
          description: 'The credential operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The credential operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation failure rate exceeds 15% for 30 minutes'
          title: '{{ $labels.cluster }}: Credential operation failure rate exceeds 15% for 30 minutes'
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
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterStuckOperation/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.phase }}'
          description: 'Credential operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          info: 'Credential operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 1 hour. Stuck operations are invisible to success/failure SLIs and require investigation.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential operation stuck in {{ $labels.phase }} for over 1 hour'
          title: '{{ $labels.cluster }}: Credential operation stuck in {{ $labels.phase }} for over 1 hour resource_id:{{ $labels.resource_id }}'
        }
        expression: '((time() - backend_resource_operation_start_time_seconds{operation_type=~"requestcredential|revokecredentials",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}) and backend_resource_operation_phase_info{operation_type=~"requestcredential|revokecredentials",phase=~"accepted|provisioning|deleting",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters"} == 1) > 3600'
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
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterSaturationQueueDepth/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Credential controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Credential controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} depth is high'
          title: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} depth is high'
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
          severity: '4'
        }
        annotations: {
          correlationId: 'userJourneyAccessClusterSaturationRetryHotLoop/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Credential controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Credential controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'aka.ms/arohcp-runbook-access-cluster'
          summary: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} retry hot loop'
          title: '{{ $labels.cluster }}: Credential controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '(sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_retries_total{name=~".*(RequestCredential|RevokeCredentials).*",namespace="aro-hcp"}[10m]))) / sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_adds_total{name=~".*(RequestCredential|RevokeCredentials).*",namespace="aro-hcp"}[10m])))) > 0.5'
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
        alert: 'UJClusterProvisionErrors1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'UJClusterProvisionErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of cluster create (install) operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours. A regional install failure of this magnitude typically points at a shared dependency (e.g. registry, DNS, or ARM) rather than individual clusters.'
          info: 'More than 72% of cluster create (install) operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours. A regional install failure of this magnitude typically points at a shared dependency (e.g. registry, DNS, or ARM) rather than individual clusters.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: '{{ $labels.cluster }}: Cluster provisioning error rate critically high (>72%)'
          title: '{{ $labels.cluster }}: Cluster provisioning error rate critically high (>72%)'
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
        alert: 'UJClusterProvisionErrors6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'UJClusterProvisionErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of cluster create (install) operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of cluster create (install) operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: '{{ $labels.cluster }}: Cluster provisioning error rate elevated (>30%) for 30+ minutes'
          title: '{{ $labels.cluster }}: Cluster provisioning error rate elevated (>30%) for 30+ minutes'
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
        alert: 'UJClusterProvisionErrors3d'
        enabled: true
        labels: {
          long_window: '3d'
          severity: '4'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'UJClusterProvisionErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of cluster create (install) operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of cluster create (install) operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: '{{ $labels.cluster }}: Cluster provisioning error rate exceeds SLO target (>5%) for 6+ hours'
          title: '{{ $labels.cluster }}: Cluster provisioning error rate exceeds SLO target (>5%) for 6+ hours'
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
        alert: 'UJClusterProvisionErrorsDegradation'
        enabled: true
        labels: {
          severity: '4'
          slo: 'cluster-provision-errors'
        }
        annotations: {
          correlationId: 'UJClusterProvisionErrorsDegradation/{{ $labels.cluster }}'
          description: 'The cluster create (install) failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The cluster create (install) failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-provision'
          summary: '{{ $labels.cluster }}: Cluster provisioning failure rate exceeds 15% for 30 minutes'
          title: '{{ $labels.cluster }}: Cluster provisioning failure rate exceeds 15% for 30 minutes'
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
        alert: 'UJNodePoolErrors1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: 'info'
          short_window: '5m'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors1h5m/{{ $labels.cluster }}'
          description: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          info: 'More than 72% of node pool operations are in failed state, indicating a fast error budget burn (14.4x) that would exhaust the 95% SLO budget in ~12 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation error rate critically high (>72%)'
          title: '{{ $labels.cluster }}: Node Pool operation error rate critically high (>72%)'
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
        alert: 'UJNodePoolErrors6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'info'
          short_window: '30m'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors6h30m/{{ $labels.cluster }}'
          description: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          info: 'More than 30% of node pool operations are in failed state sustained over 30 minutes, indicating a medium error budget burn (6x) that would exhaust the 95% SLO budget in ~28 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation error rate elevated (>30%) for 30+ minutes'
          title: '{{ $labels.cluster }}: Node Pool operation error rate elevated (>30%) for 30+ minutes'
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
        alert: 'UJNodePoolErrors3d'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'info'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrors3d/{{ $labels.cluster }}'
          description: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          info: 'More than 5% of node pool operations are in failed state sustained over 6 hours, indicating persistent degradation at the 95% SLO boundary that would exhaust the error budget in ~7 days.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
          title: '{{ $labels.cluster }}: Node Pool operation error rate exceeds SLO target (>5%) for 6+ hours'
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
        alert: 'UJNodePoolErrorsDegradation'
        enabled: true
        labels: {
          severity: 'info'
          slo: 'nodepool-errors'
        }
        annotations: {
          correlationId: 'UJNodePoolErrorsDegradation/{{ $labels.cluster }}'
          description: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          info: 'The node pool operation failure rate has been above 15% for 30 minutes. This provides early warning of degradation before SLO-based burn rate alerts fire.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation failure rate exceeds 15% for 30 minutes'
          title: '{{ $labels.cluster }}: Node Pool operation failure rate exceeds 15% for 30 minutes'
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
        alert: 'UJNodePoolStuckOperation'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'UJNodePoolStuckOperation/{{ $labels.cluster }}/{{ $labels.resource_id }}/{{ $labels.phase }}'
          description: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 2 hours. Stuck operations are invisible to success/failure SLIs and require investigation.'
          info: 'Node pool operation for {{ $labels.resource_id }} has been in {{ $labels.phase }} phase for over 2 hours. Stuck operations are invisible to success/failure SLIs and require investigation.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool operation stuck in {{ $labels.phase }} for over 2 hours'
          title: '{{ $labels.cluster }}: Node Pool operation stuck in {{ $labels.phase }} for over 2 hours resource_id:{{ $labels.resource_id }}'
        }
        expression: 'max_over_time((((time() - backend_resource_operation_start_time_seconds{resource_type=~".*nodepools",subscription_id!~"974ebd46-8ad3-41e3-afef-7ef25fd5c371|e8c5a115-842d-4d7e-98ad-cfb2c50b209e|e627aa70-36a3-40b0-8e68-975269e39d7b|6ed122d1-7e03-4a01-baae-9020abf350d4|64f0619f-ebc2-4156-9d91-c4c781de7e54|dee2f1be-a999-4e19-b027-221e7adaf7d3|8d696692-794f-4cdb-ba25-9250c9e9ec4c|ec435068-e722-475f-8504-c91b72a5dc51|403d9de9-132b-4974-94a5-5b78bdfa191e"}) and backend_resource_operation_phase_info{phase=~"updating|deleting",resource_type=~".*nodepools"} == 1) > 7200)[6h:5m])'
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
        alert: 'UJNodePoolSaturationQueueDepth'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'UJNodePoolSaturationQueueDepth/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          info: 'Node pool controller workqueue {{ $labels.name }} has had a depth > 10 for more than 5 minutes, indicating work is accumulating faster than it can be processed.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} depth is high'
          title: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} depth is high'
        }
        expression: 'max by (name, cluster) (max without (prometheus_replica) (workqueue_depth{name=~".*NodePool.*",namespace="aro-hcp"})) > 10'
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
        alert: 'UJNodePoolSaturationRetryHotLoop'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'UJNodePoolSaturationRetryHotLoop/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Node pool controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Node pool controller workqueue {{ $labels.name }} has a retry ratio > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'https://aka.ms/arohcp-runbook-nodepool'
          summary: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} retry hot loop'
          title: '{{ $labels.cluster }}: Node Pool controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '(sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_retries_total{name=~".*NodePool.*",namespace="aro-hcp"}[10m]))) / sum by (name, cluster) (max without (prometheus_replica) (rate(workqueue_adds_total{name=~".*NodePool.*",namespace="aro-hcp"}[10m])))) > 0.5'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource ingressMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'ingress-monitor-rules'
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
        alert: 'UJIngressAvailability1h5m'
        enabled: true
        labels: {
          long_window: '1h'
          severity: 'warning'
          short_window: '5m'
        }
        annotations: {
          correlationId: 'UJIngressAvailability1h5m/{{ $labels.cluster }}'
          message: 'High error budget burn for ingress {{ $labels.probe_url }} (current value: {{ $value }})'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          title: 'UJIngressAvailability1h5m'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[5m])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[5m]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[5m])) > 5 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[1h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[1h]))) > (14.4 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[1h])) > 60'
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
        alert: 'UJIngressAvailability6h30m'
        enabled: true
        labels: {
          long_window: '6h'
          severity: 'warning'
          short_window: '30m'
        }
        annotations: {
          correlationId: 'UJIngressAvailability6h30m/{{ $labels.cluster }}'
          message: 'High error budget burn for ingress {{ $labels.probe_url }} (current value: {{ $value }})'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          title: 'UJIngressAvailability6h30m'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[30m])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[30m]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[30m])) > 30 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[6h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[6h]))) > (6 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[6h])) > 360'
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
        alert: 'UJIngressAvailability1d2h'
        enabled: true
        labels: {
          long_window: '1d'
          severity: 'warning'
          short_window: '2h'
        }
        annotations: {
          correlationId: 'UJIngressAvailability1d2h/{{ $labels.cluster }}'
          message: 'High error budget burn for ingress {{ $labels.probe_url }} (current value: {{ $value }})'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          title: 'UJIngressAvailability1d2h'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[2h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[2h]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[2h])) > 120 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[1d])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[1d]))) > (3 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[1d])) > 1440'
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
        alert: 'UJIngressAvailability3d6h'
        enabled: true
        labels: {
          long_window: '3d'
          severity: 'warning'
          short_window: '6h'
        }
        annotations: {
          correlationId: 'UJIngressAvailability3d6h/{{ $labels.cluster }}'
          message: 'High error budget burn for ingress {{ $labels.probe_url }} (current value: {{ $value }})'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          title: 'UJIngressAvailability3d6h'
        }
        expression: '1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[6h])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[6h]))) > (1 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[6h])) > 360 and 1 - (sum by (probe_url, namespace, _id, cluster) (sum_over_time(probe_success{job="blackbox-ingress"}[3d])) / sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[3d]))) > (1 * (1 - 0.9995)) and sum by (probe_url, namespace, _id, cluster) (count_over_time(probe_success{job="blackbox-ingress"}[3d])) > 4320'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
