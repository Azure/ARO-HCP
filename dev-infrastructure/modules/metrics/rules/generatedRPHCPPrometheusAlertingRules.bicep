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

resource arohcpIngressAvailabilitySloAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_ingress_availability_slo_alerts'
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
        alert: 'userJourneyIngressAvailabilityErrors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          severity: '4'
          short_window: '5m'
          slo: 'ingress-availability'
        }
        annotations: {
          correlationId: 'userJourneyIngressAvailabilityErrors/{{ $labels._id }}'
          description: '''More than 7.2% of synthetic canary route observations failed in both the last 5 minutes and the last hour for HCP cluster {{ $labels._id }}, indicating an active fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.
Resource ID: {{ $labels.resource_id }}
'''
          info: '''More than 7.2% of synthetic canary route observations failed in both the last 5 minutes and the last hour for HCP cluster {{ $labels._id }}, indicating an active fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.
Resource ID: {{ $labels.resource_id }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary availability critically degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
          title: 'Ingress canary availability critically degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
        }
        expression: '((errors:ingress_canary:total_5m >= 3 and errors:ingress_canary:error_rate_5m > 0.072) and (errors:ingress_canary:total_1h >= 10 and errors:ingress_canary:error_rate_1h > 0.072)) * on (_id) group_left (resource_id) group by (_id, resource_id, region) (hostedClusterAPI_kubeapiserver_available * 0 + 1)'
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
        alert: 'userJourneyIngressAvailabilityErrors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: '4'
          short_window: '30m'
          slo: 'ingress-availability'
        }
        annotations: {
          correlationId: 'userJourneyIngressAvailabilityErrors/{{ $labels._id }}'
          description: '''More than 3% of synthetic canary route observations failed in both the last 30 minutes and the last 6 hours for HCP cluster {{ $labels._id }}, indicating an active medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.
Resource ID: {{ $labels.resource_id }}
'''
          info: '''More than 3% of synthetic canary route observations failed in both the last 30 minutes and the last 6 hours for HCP cluster {{ $labels._id }}, indicating an active medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.
Resource ID: {{ $labels.resource_id }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary availability degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
          title: 'Ingress canary availability degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
        }
        expression: '((errors:ingress_canary:total_30m >= 15 and errors:ingress_canary:error_rate_30m > 0.03) and (errors:ingress_canary:total_6h >= 30 and errors:ingress_canary:error_rate_6h > 0.03)) * on (_id) group_left (resource_id) group by (_id, resource_id, region) (hostedClusterAPI_kubeapiserver_available * 0 + 1)'
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
        alert: 'userJourneyIngressAvailabilityErrors3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
          slo: 'ingress-availability'
        }
        annotations: {
          correlationId: 'userJourneyIngressAvailabilityErrors/{{ $labels._id }}'
          description: '''More than 0.5% of synthetic canary route observations failed in both the last 6 hours and the last 3 days for HCP cluster {{ $labels._id }}, consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.
Resource ID: {{ $labels.resource_id }}
'''
          info: '''More than 0.5% of synthetic canary route observations failed in both the last 6 hours and the last 3 days for HCP cluster {{ $labels._id }}, consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.
Resource ID: {{ $labels.resource_id }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary availability trending below SLO for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
          title: 'Ingress canary availability trending below SLO for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
        }
        expression: '((errors:ingress_canary:total_6h >= 30 and errors:ingress_canary:error_rate_6h > 0.005) and (errors:ingress_canary:total_3d >= 60 and errors:ingress_canary:error_rate_3d > 0.005)) * on (_id) group_left (resource_id) group by (_id, resource_id, region) (hostedClusterAPI_kubeapiserver_available * 0 + 1)'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpIngressLatencySloAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_ingress_latency_slo_alerts'
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
        alert: 'userJourneyIngressLatencyP99Errors1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          severity: '4'
          short_window: '5m'
          slo: 'ingress-latency'
        }
        annotations: {
          correlationId: 'userJourneyIngressLatencyP99Errors/{{ $labels._id }}'
          description: '''More than 7.2% of synthetic canary route checks exceeded 200ms in both the last 5 minutes and the last hour for HCP cluster {{ $labels._id }}, indicating an active fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.
Resource ID: {{ $labels.resource_id }}
'''
          info: '''More than 7.2% of synthetic canary route checks exceeded 200ms in both the last 5 minutes and the last hour for HCP cluster {{ $labels._id }}, indicating an active fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.
Resource ID: {{ $labels.resource_id }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary latency critically degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
          title: 'Ingress canary latency critically degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
        }
        expression: '((errors:ingress_canary_latency:total_5m >= 3 and errors:ingress_canary_latency:error_rate_5m > 0.072) and (errors:ingress_canary_latency:total_1h >= 10 and errors:ingress_canary_latency:error_rate_1h > 0.072)) * on (_id) group_left (resource_id) group by (_id, resource_id, region) (hostedClusterAPI_kubeapiserver_available * 0 + 1)'
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
        alert: 'userJourneyIngressLatencyP99Errors6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: '4'
          short_window: '30m'
          slo: 'ingress-latency'
        }
        annotations: {
          correlationId: 'userJourneyIngressLatencyP99Errors/{{ $labels._id }}'
          description: '''More than 3% of synthetic canary route checks exceeded 200ms in both the last 30 minutes and the last 6 hours for HCP cluster {{ $labels._id }}, indicating an active medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.
Resource ID: {{ $labels.resource_id }}
'''
          info: '''More than 3% of synthetic canary route checks exceeded 200ms in both the last 30 minutes and the last 6 hours for HCP cluster {{ $labels._id }}, indicating an active medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.
Resource ID: {{ $labels.resource_id }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary latency degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
          title: 'Ingress canary latency degraded for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
        }
        expression: '((errors:ingress_canary_latency:total_30m >= 15 and errors:ingress_canary_latency:error_rate_30m > 0.03) and (errors:ingress_canary_latency:total_6h >= 30 and errors:ingress_canary_latency:error_rate_6h > 0.03)) * on (_id) group_left (resource_id) group by (_id, resource_id, region) (hostedClusterAPI_kubeapiserver_available * 0 + 1)'
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
        alert: 'userJourneyIngressLatencyP99Errors3d6h'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          severity: '4'
          short_window: '6h'
          slo: 'ingress-latency'
        }
        annotations: {
          correlationId: 'userJourneyIngressLatencyP99Errors/{{ $labels._id }}'
          description: '''More than 0.5% of synthetic canary route checks exceeded 200ms in both the last 6 hours and the last 3 days for HCP cluster {{ $labels._id }}, consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.
Resource ID: {{ $labels.resource_id }}
'''
          info: '''More than 0.5% of synthetic canary route checks exceeded 200ms in both the last 6 hours and the last 3 days for HCP cluster {{ $labels._id }}, consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.
Resource ID: {{ $labels.resource_id }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary latency trending below SLO for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
          title: 'Ingress canary latency trending below SLO for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
        }
        expression: '((errors:ingress_canary_latency:total_6h >= 30 and errors:ingress_canary_latency:error_rate_6h > 0.005) and (errors:ingress_canary_latency:total_3d >= 60 and errors:ingress_canary_latency:error_rate_3d > 0.005)) * on (_id) group_left (resource_id) group by (_id, resource_id, region) (hostedClusterAPI_kubeapiserver_available * 0 + 1)'
        for: 'PT3H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpIngressCanaryLivenessAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_ingress_canary_liveness_alerts'
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
        alert: 'userJourneyIngressCanaryAbsent'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'ingress-availability'
        }
        annotations: {
          correlationId: 'userJourneyIngressCanaryAbsent/{{ $labels._id }}'
          description: '''HCP cluster {{ $labels._id }} was emitting synthetic canary route observations but has reported none in the last 10 minutes. This is a loss of the ingress availability/latency signal, not a confirmed failure -- investigate whether ingress-operator or the canary route itself is down.
Resource ID: {{ $labels.resource_id }}
'''
          info: '''HCP cluster {{ $labels._id }} was emitting synthetic canary route observations but has reported none in the last 10 minutes. This is a loss of the ingress availability/latency signal, not a confirmed failure -- investigate whether ingress-operator or the canary route itself is down.
Resource ID: {{ $labels.resource_id }}
'''
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary has stopped reporting for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
          title: 'Ingress canary has stopped reporting for {{ $labels._id }} (resource_id: {{ $labels.resource_id }})'
        }
        expression: '(count by (_id, cluster, region) (max_over_time(ingress_canary_route_reachable[1w])) * on (_id) group_left (resource_id) group by (_id, resource_id, region) (hostedClusterAPI_kubeapiserver_available * 0 + 1)) unless on (_id, cluster, region) count by (_id, cluster, region) (max_over_time(ingress_canary_route_reachable[10m]))'
        for: 'PT10M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
