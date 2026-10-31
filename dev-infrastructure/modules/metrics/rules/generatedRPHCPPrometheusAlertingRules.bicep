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
        expression: '(1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_5m_excl_known / hostedClusterAPI_kubeapiserver_available:sli_count_5m_excl_known) > (14.4 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_5m_excl_known > 3) and on (name, namespace, _id, resource_id, cluster) (1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_1h_excl_known / hostedClusterAPI_kubeapiserver_available:sli_count_1h_excl_known) > (14.4 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_1h_excl_known > 54)'
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
        expression: '(1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_30m_excl_known / hostedClusterAPI_kubeapiserver_available:sli_count_30m_excl_known) > (6 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_30m_excl_known > 27) and on (name, namespace, _id, resource_id, cluster) (1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_6h_excl_known / hostedClusterAPI_kubeapiserver_available:sli_count_6h_excl_known) > (6 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_6h_excl_known > 64)'
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
        expression: '(1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_6h_excl_known / hostedClusterAPI_kubeapiserver_available:sli_count_6h_excl_known) > (1 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_6h_excl_known > 64) and on (name, namespace, _id, resource_id, cluster) (1 - (hostedClusterAPI_kubeapiserver_available:sli_sum_3d_excl_known / hostedClusterAPI_kubeapiserver_available:sli_count_3d_excl_known) > (1 * (1 - 0.9995)) and on (name, namespace, _id, resource_id, cluster) hostedClusterAPI_kubeapiserver_available:sli_count_3d_excl_known > 130)'
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

resource rpUserjourneyKasKmsSystemicFailureRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-userjourney-kas-kms-systemic-failure-rules'
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
        alert: 'userJourneyKubeApiserverAvailabilityKmsSystemicFailure'
        enabled: true
        labels: {
          component: 'slo'
          scope: 'mgmt-cluster'
          severity: '3'
          slo: 'kas-availability'
        }
        annotations: {
          correlationId: 'userJourneyKubeApiserverAvailabilityKmsSystemicFailure/{{ $labels.cluster }}'
          description: 'At least half (minimum 3) of the hosted clusters on management cluster {{ $labels.cluster }} are not reporting ValidAzureKMSConfig=True ({{ $value | humanizePercentage }} affected) -- independent customer BYOK failures don\'t correlate like this, so treat it as a shared Azure dependency failure (management cluster KMS managed identity / IMDS, control-plane-operator rollout, or a regional Key Vault / AAD outage), not a per-customer issue. This is NOT per-cluster actionable: confirm the blast radius and declare an incident if confirmed. Per-cluster KMS failures are already excluded from the userJourneyKubeApiserverAvailability burn-rate alerts; this is the only notification path for the systemic case.'
          info: 'At least half (minimum 3) of the hosted clusters on management cluster {{ $labels.cluster }} are not reporting ValidAzureKMSConfig=True ({{ $value | humanizePercentage }} affected) -- independent customer BYOK failures don\'t correlate like this, so treat it as a shared Azure dependency failure (management cluster KMS managed identity / IMDS, control-plane-operator rollout, or a regional Key Vault / AAD outage), not a per-customer issue. This is NOT per-cluster actionable: confirm the blast radius and declare an incident if confirmed. Per-cluster KMS failures are already excluded from the userJourneyKubeApiserverAvailability burn-rate alerts; this is the only notification path for the systemic case.'
          runbook_url: 'https://aka.ms/arohcp-runbook-ujkasavailable-kms-systemic'
          summary: '[HCPKASAvailableKMSSystemic] Azure KMS failing across hosted clusters on {{ $labels.cluster }}'
          title: '[HCPKASAvailableKMSSystemic] Azure KMS failing across hosted clusters on {{ $labels.cluster }}'
        }
        expression: '(count by (cluster, region) (max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_valid_azure_kms_config{status!="True"}) == 1) / count by (cluster, region) (max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_valid_azure_kms_config) == 1)) >= 0.5 and on (cluster) count by (cluster, region) (max by (name, namespace, _id, resource_id, cluster, region) (hostedClusterAPI_valid_azure_kms_config{status!="True"}) == 1) >= 3'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
