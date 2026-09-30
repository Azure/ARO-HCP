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
        alert: 'userJourneyIngressAvailability1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'ingress-availability'
        }
        annotations: {
          correlationId: '{{ $labels._id }}'
          description: 'More than 7.2% of synthetic canary route observations failed over the last hour for HCP cluster {{ $labels._id }} (at least 10 observations), indicating a fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.'
          info: 'More than 7.2% of synthetic canary route observations failed over the last hour for HCP cluster {{ $labels._id }} (at least 10 observations), indicating a fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary availability critically degraded for {{ $labels._id }}'
          title: 'Ingress canary availability critically degraded for {{ $labels._id }}'
        }
        expression: 'errors:ingress_canary:total_1h >= 10 and errors:ingress_canary:error_rate_1h > 0.072'
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
        alert: 'userJourneyIngressAvailability6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'ingress-availability'
        }
        annotations: {
          correlationId: '{{ $labels._id }}'
          description: 'More than 3% of synthetic canary route observations failed over the last 6 hours for HCP cluster {{ $labels._id }} (at least 30 observations), indicating a medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.'
          info: 'More than 3% of synthetic canary route observations failed over the last 6 hours for HCP cluster {{ $labels._id }} (at least 30 observations), indicating a medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary availability degraded for {{ $labels._id }}'
          title: 'Ingress canary availability degraded for {{ $labels._id }}'
        }
        expression: 'errors:ingress_canary:total_6h >= 30 and errors:ingress_canary:error_rate_6h > 0.03'
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
        alert: 'userJourneyIngressAvailability3d'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          severity: '4'
          slo: 'ingress-availability'
        }
        annotations: {
          correlationId: '{{ $labels._id }}'
          description: 'More than 0.5% of synthetic canary route observations failed over the last 3 days for HCP cluster {{ $labels._id }} (at least 60 observations and 2 failures), consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.'
          info: 'More than 0.5% of synthetic canary route observations failed over the last 3 days for HCP cluster {{ $labels._id }} (at least 60 observations and 2 failures), consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary availability trending below SLO for {{ $labels._id }}'
          title: 'Ingress canary availability trending below SLO for {{ $labels._id }}'
        }
        expression: 'errors:ingress_canary:total_3d >= 60 and errors:ingress_canary:failed_3d >= 2 and errors:ingress_canary:error_rate_3d > 0.005'
        for: 'PT6H'
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
        alert: 'userJourneyIngressLatency1h5m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '1h'
          severity: '3'
          short_window: '5m'
          slo: 'ingress-latency'
        }
        annotations: {
          correlationId: '{{ $labels._id }}'
          description: 'More than 7.2% of synthetic canary route checks exceeded 200ms over the last hour for HCP cluster {{ $labels._id }} (at least 10 checks), indicating a fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.'
          info: 'More than 7.2% of synthetic canary route checks exceeded 200ms over the last hour for HCP cluster {{ $labels._id }} (at least 10 checks), indicating a fast error budget burn (14.4x) that would exhaust the 99.5% SLO budget in ~50 hours.'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary latency critically degraded for {{ $labels._id }}'
          title: 'Ingress canary latency critically degraded for {{ $labels._id }}'
        }
        expression: 'errors:ingress_canary_latency:total_1h >= 10 and errors:ingress_canary_latency:error_rate_1h > 0.072'
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
        alert: 'userJourneyIngressLatency6h30m'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '6h'
          severity: '3'
          short_window: '30m'
          slo: 'ingress-latency'
        }
        annotations: {
          correlationId: '{{ $labels._id }}'
          description: 'More than 3% of synthetic canary route checks exceeded 200ms over the last 6 hours for HCP cluster {{ $labels._id }} (at least 30 checks), indicating a medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.'
          info: 'More than 3% of synthetic canary route checks exceeded 200ms over the last 6 hours for HCP cluster {{ $labels._id }} (at least 30 checks), indicating a medium error budget burn (6x) that would exhaust the 99.5% SLO budget in ~5 days.'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary latency degraded for {{ $labels._id }}'
          title: 'Ingress canary latency degraded for {{ $labels._id }}'
        }
        expression: 'errors:ingress_canary_latency:total_6h >= 30 and errors:ingress_canary_latency:error_rate_6h > 0.03'
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
        alert: 'userJourneyIngressLatency3d'
        enabled: true
        labels: {
          component: 'slo'
          long_window: '3d'
          severity: '4'
          slo: 'ingress-latency'
        }
        annotations: {
          correlationId: '{{ $labels._id }}'
          description: 'More than 0.5% of synthetic canary route checks exceeded 200ms over the last 3 days for HCP cluster {{ $labels._id }} (at least 60 checks and 2 slow checks), consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.'
          info: 'More than 0.5% of synthetic canary route checks exceeded 200ms over the last 3 days for HCP cluster {{ $labels._id }} (at least 60 checks and 2 slow checks), consuming the error budget at the SLO rate. No immediate customer impact but trend requires investigation.'
          runbook_url: 'https://aka.ms/arohcp-runbook-ingress'
          summary: 'Ingress canary latency trending below SLO for {{ $labels._id }}'
          title: 'Ingress canary latency trending below SLO for {{ $labels._id }}'
        }
        expression: 'errors:ingress_canary_latency:total_3d >= 60 and errors:ingress_canary_latency:slow_3d >= 2 and errors:ingress_canary_latency:error_rate_3d > 0.005'
        for: 'PT6H'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
