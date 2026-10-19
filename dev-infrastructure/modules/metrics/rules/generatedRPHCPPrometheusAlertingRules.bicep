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

resource hypershiftJourneyAvailabilityMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hypershift-journey-availability-monitor-rules'
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
        alert: 'userJourneyAvailability1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '5m'
          slo: 'hypershift-journey-availability'
        }
        annotations: {
          correlationId: 'userJourneyAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Hosted control plane API availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          info: '''Hosted control plane API availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: Hosted control plane API availability is burning error budget at 14.4x over 1h/5m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: Hosted control plane API availability is burning error budget at 14.4x over 1h/5m'
        }
        expression: '((1 - (hypershift_kube_apiserver_available:sli_sum_1h / hypershift_kube_apiserver_available:sli_count_1h)) > (14.4 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_1h > 54) and on (namespace, cluster) ((1 - (hypershift_kube_apiserver_available:sli_sum_5m / hypershift_kube_apiserver_available:sli_count_5m)) > (14.4 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_5m > 3)'
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
        alert: 'userJourneyAvailability6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '30m'
          slo: 'hypershift-journey-availability'
        }
        annotations: {
          correlationId: 'userJourneyAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Hosted control plane API availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          info: '''Hosted control plane API availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: Hosted control plane API availability is burning error budget at 6x over 6h/30m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: Hosted control plane API availability is burning error budget at 6x over 6h/30m'
        }
        expression: '((1 - (hypershift_kube_apiserver_available:sli_sum_6h / hypershift_kube_apiserver_available:sli_count_6h)) > (6 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_6h > 320) and on (namespace, cluster) ((1 - (hypershift_kube_apiserver_available:sli_sum_30m / hypershift_kube_apiserver_available:sli_count_30m)) > (6 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_30m > 27)'
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
        alert: 'userJourneyAvailability3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'hypershift-journey'
          severity: '3'
          short_window: '6h'
          slo: 'hypershift-journey-availability'
        }
        annotations: {
          correlationId: 'userJourneyAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''Hosted control plane API availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          info: '''Hosted control plane API availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Control Plane Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: Hosted control plane API availability is burning error budget at 1x over 3d/6h'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: Hosted control plane API availability is burning error budget at 1x over 3d/6h'
        }
        expression: '((1 - (hypershift_kube_apiserver_available:sli_sum_3d / hypershift_kube_apiserver_available:sli_count_3d)) > (1 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_3d > 3900) and on (namespace, cluster) ((1 - (hypershift_kube_apiserver_available:sli_sum_6h / hypershift_kube_apiserver_available:sli_count_6h)) > (1 * (1 - 0.9995)) and on (namespace, cluster) hypershift_kube_apiserver_available:sli_count_6h > 320)'
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
        alert: 'hypershiftCPOAvailability1h5m'
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
          correlationId: 'hypershiftCPOAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator replica availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator replica availability is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator replica availability is burning error budget at 14.4x over 1h/5m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator replica availability is burning error budget at 14.4x over 1h/5m'
        }
        expression: '((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[1h])) > (14.4 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[1h:1m]) > 54) and on (cluster, namespace) ((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[5m])) > (14.4 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[5m:1m]) > 3)'
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
        alert: 'hypershiftCPOAvailability6h30m'
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
          correlationId: 'hypershiftCPOAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator replica availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator replica availability is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator replica availability is burning error budget at 6x over 6h/30m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator replica availability is burning error budget at 6x over 6h/30m'
        }
        expression: '((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[6h])) > (6 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[6h:5m]) > 64) and on (cluster, namespace) ((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[30m])) > (6 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[30m:1m]) > 27)'
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
        alert: 'hypershiftCPOAvailability3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-availability'
        }
        annotations: {
          correlationId: 'hypershiftCPOAvailability/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator replica availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator replica availability is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator replica availability is burning error budget at 1x over 3d/6h'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator replica availability is burning error budget at 1x over 3d/6h'
        }
        expression: '((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[3d])) > (1 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[3d:30m]) > 130) and on (cluster, namespace) ((1 - avg_over_time(hypershift:cpo:replica_availability:ratio[6h])) > (1 * (1 - 0.9995)) and on (cluster, namespace) count_over_time(hypershift:cpo:replica_availability:ratio[6h:5m]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOReconcileErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconcile error ratio is burning error budget at 14.4x over 1h/5m'
        }
        expression: '(hypershift:cpo:reconcile_error:ratio_rate1h > (14.4 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[1h:1m]) > 54) and on (cluster, namespace) (hypershift:cpo:reconcile_error:ratio_rate5m > (14.4 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[5m:1m]) > 3)'
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
        alert: 'hypershiftCPOReconcileErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error ratio is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error ratio is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconcile error ratio is burning error budget at 6x over 6h/30m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconcile error ratio is burning error budget at 6x over 6h/30m'
        }
        expression: '(hypershift:cpo:reconcile_error:ratio_rate6h > (6 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[6h:5m]) > 64) and on (cluster, namespace) (hypershift:cpo:reconcile_error:ratio_rate30m > (6 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[30m:1m]) > 27)'
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
        alert: 'hypershiftCPOReconcileErrors3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-errors'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconcile error ratio is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconcile error ratio is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconcile error ratio is burning error budget at 1x over 3d/6h'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconcile error ratio is burning error budget at 1x over 3d/6h'
        }
        expression: '(hypershift:cpo:reconcile_error:ratio_rate3d > (1 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[3d:30m]) > 130) and on (cluster, namespace) (hypershift:cpo:reconcile_error:ratio_rate6h > (1 * (1 - 0.999)) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[6h:5m]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOReconcileLatency1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconciles slower than 10s is burning error budget at 14.4x over 1h/5m'
        }
        expression: '(hypershift:cpo:reconcile_slow:ratio_rate1h > (14.4 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[1h:1m]) > 54) and on (cluster, namespace) (hypershift:cpo:reconcile_slow:ratio_rate5m > (14.4 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[5m:1m]) > 3)'
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
        alert: 'hypershiftCPOReconcileLatency6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconciles slower than 10s is burning error budget at 6x over 6h/30m'
        }
        expression: '(hypershift:cpo:reconcile_slow:ratio_rate6h > (6 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[6h:5m]) > 64) and on (cluster, namespace) (hypershift:cpo:reconcile_slow:ratio_rate30m > (6 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[30m:1m]) > 27)'
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
        alert: 'hypershiftCPOReconcileLatency3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-latency'
        }
        annotations: {
          correlationId: 'hypershiftCPOReconcileLatency/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator reconciles slower than 10s is burning error budget at 1x over 3d/6h'
        }
        expression: '(hypershift:cpo:reconcile_slow:ratio_rate3d > (1 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[3d:30m]) > 130) and on (cluster, namespace) (hypershift:cpo:reconcile_slow:ratio_rate6h > (1 * 0.01) and on (cluster, namespace) hypershift:cpo:reconcile:rate5m > 0 and on (cluster, namespace) count_over_time(hypershift:cpo:reconcile:rate5m[6h:5m]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOSaturation1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '5m'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturation/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator saturated intervals is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator saturated intervals is burning error budget at 14.4x over 1h/5m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator saturated intervals is burning error budget at 14.4x over 1h/5m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator saturated intervals is burning error budget at 14.4x over 1h/5m'
        }
        expression: '(avg_over_time(hypershift:cpo:saturated[1h]) > (14.4 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[1h:1m]) > 54) and on (cluster, namespace) (avg_over_time(hypershift:cpo:saturated[5m]) > (14.4 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[5m:1m]) > 3)'
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
        alert: 'hypershiftCPOSaturation6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '30m'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturation/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator saturated intervals is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator saturated intervals is burning error budget at 6x over 6h/30m.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator saturated intervals is burning error budget at 6x over 6h/30m'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator saturated intervals is burning error budget at 6x over 6h/30m'
        }
        expression: '(avg_over_time(hypershift:cpo:saturated[6h]) > (6 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[6h:5m]) > 64) and on (cluster, namespace) (avg_over_time(hypershift:cpo:saturated[30m]) > (6 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[30m:1m]) > 27)'
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
        alert: 'hypershiftCPOSaturation3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          service: 'control-plane-operator'
          severity: '3'
          short_window: '6h'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturation/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator saturated intervals is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator saturated intervals is burning error budget at 1x over 3d/6h.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator saturated intervals is burning error budget at 1x over 3d/6h'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator saturated intervals is burning error budget at 1x over 3d/6h'
        }
        expression: '(avg_over_time(hypershift:cpo:saturated[3d]) > (1 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[3d:30m]) > 130) and on (cluster, namespace) (avg_over_time(hypershift:cpo:saturated[6h]) > (1 * 0.01) and on (cluster, namespace) count_over_time(hypershift:cpo:saturated[6h:5m]) > 64)'
        for: 'PT3H'
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
        alert: 'hypershiftCPOSaturationSignal'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '4'
          slo: 'control-plane-operator-saturation'
        }
        annotations: {
          correlationId: 'hypershiftCPOSaturationSignal/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.signal }}'
          description: '''control-plane-operator {{ $labels.signal }} is above its limit.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator {{ $labels.signal }} is above its limit.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator {{ $labels.signal }} is above its limit'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator {{ $labels.signal }} is above its limit'
        }
        expression: 'hypershift:cpo:saturation_signal == 1'
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
        alert: 'hypershiftCPOIdle'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '4'
          slo: 'control-plane-operator-traffic'
        }
        annotations: {
          correlationId: 'hypershiftCPOIdle/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator has not reconciled anything for 30 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator has not reconciled anything for 30 minutes.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator has not reconciled anything for 30 minutes'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator has not reconciled anything for 30 minutes'
        }
        expression: 'hypershift:cpo:reconcile:rate5m == 0'
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
        alert: 'hypershiftCPOQueueSurge'
        enabled: true
        labels: {
          component: 'slo'
          service: 'control-plane-operator'
          severity: '4'
          slo: 'control-plane-operator-traffic'
        }
        annotations: {
          correlationId: 'hypershiftCPOQueueSurge/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: '''control-plane-operator workqueue adds exceed 3x the rate one hour ago.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          info: '''control-plane-operator workqueue adds exceed 3x the rate one hour ago.
Management Cluster: {{ $labels.cluster }}
Namespace: {{ $labels.namespace }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-hypershift'
          summary: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator workqueue adds exceed 3x the rate one hour ago'
          title: '{{ $labels.cluster }}/{{ $labels.namespace }}: control-plane-operator workqueue adds exceed 3x the rate one hour ago'
        }
        expression: 'hypershift:cpo:workqueue_adds:rate5m > 3 * (hypershift:cpo:workqueue_adds:rate5m offset 1h) and (hypershift:cpo:workqueue_adds:rate5m offset 1h) > 0 and hypershift:cpo:workqueue_adds:rate5m > 1'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
