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
        alert: 'userJourneyControlPlaneOperatorAvailabilityDown'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '3'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'userJourneyControlPlaneOperatorAvailabilityDown/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator has had no available pods for 10 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator has had no available pods for 10 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.namespace }}: control-plane-operator has no available pods'
          title: '{{ $labels.namespace }}: control-plane-operator has no available pods'
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

resource hypershiftCpoAvailabilityAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-cpo-availability-alerts'
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
        alert: 'userJourneyControlPlaneOperatorAvailability1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'userJourneyControlPlaneOperatorAvailability1h5m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator unavailable replica share is above 0.72% over both the 1h and 5m windows,
a 14.4x burn of the proposed 99.95% replica availability SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator unavailable replica share is above 0.72% over both the 1h and 5m windows,
a 14.4x burn of the proposed 99.95% replica availability SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.namespace }}: control-plane-operator availability burning error budget (14.4x)'
          title: '{{ $labels.namespace }}: control-plane-operator availability burning error budget (14.4x)'
        }
        expression: 'avg_over_time(hypershift:cpo:replicas_unavailable:ratio[1h]) > 0.0072 and on (cluster, namespace) avg_over_time(hypershift:cpo:replicas_unavailable:ratio[5m]) > 0.0072'
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
        alert: 'userJourneyControlPlaneOperatorAvailability6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'userJourneyControlPlaneOperatorAvailability6h30m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator unavailable replica share is above 0.3% over both the 6h and 30m windows,
a 6x burn of the proposed 99.95% replica availability SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator unavailable replica share is above 0.3% over both the 6h and 30m windows,
a 6x burn of the proposed 99.95% replica availability SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.namespace }}: control-plane-operator availability burning error budget (6x)'
          title: '{{ $labels.namespace }}: control-plane-operator availability burning error budget (6x)'
        }
        expression: 'avg_over_time(hypershift:cpo:replicas_unavailable:ratio[6h]) > 0.003 and on (cluster, namespace) avg_over_time(hypershift:cpo:replicas_unavailable:ratio[30m]) > 0.003'
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
        alert: 'userJourneyControlPlaneOperatorAvailability3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '4'
          short_window: '6h'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'userJourneyControlPlaneOperatorAvailability3d6h/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator unavailable replica share is above 0.05% over both the 3d and 6h windows,
a 1x burn of the proposed 99.95% replica availability SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator unavailable replica share is above 0.05% over both the 3d and 6h windows,
a 1x burn of the proposed 99.95% replica availability SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.namespace }}: control-plane-operator availability burning error budget (1x)'
          title: '{{ $labels.namespace }}: control-plane-operator availability burning error budget (1x)'
        }
        expression: 'avg_over_time(hypershift:cpo:replicas_unavailable:ratio[3d]) > 0.0005 and on (cluster, namespace) avg_over_time(hypershift:cpo:replicas_unavailable:ratio[6h]) > 0.0005'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource hypershiftCpoErrorAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-cpo-error-alerts'
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
        alert: 'userJourneyControlPlaneOperatorErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-reconcile-errors'
        }
        annotations: {
          correlationId: 'userJourneyControlPlaneOperatorErrors1h5m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error share is above 1.44% over both the 1h and 5m windows,
a 14.4x burn of the proposed 99.9% Successful Reconciliations SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error share is above 1.44% over both the 1h and 5m windows,
a 14.4x burn of the proposed 99.9% Successful Reconciliations SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.namespace }}: control-plane-operator reconcile errors burning error budget (14.4x)'
          title: '{{ $labels.namespace }}: control-plane-operator reconcile errors burning error budget (14.4x)'
        }
        expression: '(avg_over_time(hypershift:cpo:reconcile_errors:rate5m[1h]) / avg_over_time(hypershift:cpo:reconcile:rate5m[1h])) > 0.0144 and on (cluster, namespace) (avg_over_time(hypershift:cpo:reconcile_errors:rate5m[5m]) / avg_over_time(hypershift:cpo:reconcile:rate5m[5m])) > 0.0144'
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
        alert: 'userJourneyControlPlaneOperatorErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-reconcile-errors'
        }
        annotations: {
          correlationId: 'userJourneyControlPlaneOperatorErrors6h30m/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error share is above 0.6% over both the 6h and 30m windows,
a 6x burn of the proposed 99.9% Successful Reconciliations SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error share is above 0.6% over both the 6h and 30m windows,
a 6x burn of the proposed 99.9% Successful Reconciliations SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.namespace }}: control-plane-operator reconcile errors burning error budget (6x)'
          title: '{{ $labels.namespace }}: control-plane-operator reconcile errors burning error budget (6x)'
        }
        expression: '(avg_over_time(hypershift:cpo:reconcile_errors:rate5m[6h]) / avg_over_time(hypershift:cpo:reconcile:rate5m[6h])) > 0.006 and on (cluster, namespace) (avg_over_time(hypershift:cpo:reconcile_errors:rate5m[30m]) / avg_over_time(hypershift:cpo:reconcile:rate5m[30m])) > 0.006'
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
        alert: 'userJourneyControlPlaneOperatorErrors3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '4'
          short_window: '6h'
          slo: 'control-plane-operator-reconcile-errors'
        }
        annotations: {
          correlationId: 'userJourneyControlPlaneOperatorErrors3d6h/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error share is above 0.1% over both the 3d and 6h windows,
a 1x burn of the proposed 99.9% Successful Reconciliations SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error share is above 0.1% over both the 3d and 6h windows,
a 1x burn of the proposed 99.9% Successful Reconciliations SLO.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.namespace }}: control-plane-operator reconcile errors burning error budget (1x)'
          title: '{{ $labels.namespace }}: control-plane-operator reconcile errors burning error budget (1x)'
        }
        expression: '(avg_over_time(hypershift:cpo:reconcile_errors:rate5m[3d]) / avg_over_time(hypershift:cpo:reconcile:rate5m[3d])) > 0.001 and on (cluster, namespace) (avg_over_time(hypershift:cpo:reconcile_errors:rate5m[6h]) / avg_over_time(hypershift:cpo:reconcile:rate5m[6h])) > 0.001'
        for: 'PT1H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
