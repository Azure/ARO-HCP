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

resource arohcpClusterAutoscalerHealthAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cluster_autoscaler_health_alerts'
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
        alert: 'UserJourneyClusterAutoscalerStalled'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-autoscaler-health'
        }
        annotations: {
          correlationId: 'UserJourneyClusterAutoscalerStalled/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has not completed a main loop iteration in over 10 minutes, sustained for 15 minutes. The autoscaler is wedged or its pod is not running; no scaling is happening for this hosted cluster. This is an autoscaler malfunction, independent of the customer\'s autoscaling configuration.'
          info: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has not completed a main loop iteration in over 10 minutes, sustained for 15 minutes. The autoscaler is wedged or its pod is not running; no scaling is happening for this hosted cluster. This is an autoscaler malfunction, independent of the customer\'s autoscaling configuration.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-autoscaler'
          summary: '{{ $labels.namespace }}: cluster autoscaler main loop stalled'
          title: '{{ $labels.namespace }}: cluster autoscaler main loop stalled'
        }
        expression: 'health:cluster_autoscaler:seconds_since_last_activity > 600'
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
        alert: 'UserJourneyClusterAutoscalerSafetyDisabled'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-autoscaler-health'
        }
        annotations: {
          correlationId: 'UserJourneyClusterAutoscalerSafetyDisabled/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has reported cluster_safe_to_autoscale=0 for over 15 minutes. While unsafe, the autoscaler makes no scaling decisions. This is usually caused by unready, unregistered, or long-unregistered nodes and requires SRE investigation of node health.'
          info: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has reported cluster_safe_to_autoscale=0 for over 15 minutes. While unsafe, the autoscaler makes no scaling decisions. This is usually caused by unready, unregistered, or long-unregistered nodes and requires SRE investigation of node health.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-autoscaler'
          summary: '{{ $labels.namespace }}: cluster autoscaler is not safe to autoscale'
          title: '{{ $labels.namespace }}: cluster autoscaler is not safe to autoscale'
        }
        expression: 'health:cluster_autoscaler:safe_to_autoscale == 0'
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
        alert: 'UserJourneyClusterAutoscalerErrors'
        enabled: true
        labels: {
          component: 'slo'
          severity: '4'
          slo: 'cluster-autoscaler-health'
        }
        annotations: {
          correlationId: 'UserJourneyClusterAutoscalerErrors/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has been recording internal errors (cluster_autoscaler_errors_total) for over 15 minutes. This points at a malfunction in the scaling controller (cloud provider or API errors) that an SRE should investigate; it is independent of the customer\'s autoscaling configuration.'
          info: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has been recording internal errors (cluster_autoscaler_errors_total) for over 15 minutes. This points at a malfunction in the scaling controller (cloud provider or API errors) that an SRE should investigate; it is independent of the customer\'s autoscaling configuration.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-autoscaler'
          summary: '{{ $labels.namespace }}: cluster autoscaler is reporting internal errors'
          title: '{{ $labels.namespace }}: cluster autoscaler is reporting internal errors'
        }
        expression: 'errors:cluster_autoscaler:errors:rate15m > 0'
        for: 'PT15M'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource arohcpClusterAutoscalerScalingAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'arohcp_cluster_autoscaler_scaling_alerts'
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
        alert: 'UserJourneyClusterAutoscalerScaleUpFailures'
        enabled: true
        labels: {
          component: 'slo'
          severity: '3'
          slo: 'cluster-autoscaler-scaling'
        }
        annotations: {
          correlationId: 'UserJourneyClusterAutoscalerScaleUpFailures/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has been recording failed scale-up attempts for over 15 minutes. Common causes are Azure quota exhaustion, lack of capacity in the target zone/VM size, or provider errors — all SRE/infra concerns. Pending pods will not receive new nodes until this clears.'
          info: 'The cluster autoscaler for hosted cluster {{ $labels.namespace }} has been recording failed scale-up attempts for over 15 minutes. Common causes are Azure quota exhaustion, lack of capacity in the target zone/VM size, or provider errors — all SRE/infra concerns. Pending pods will not receive new nodes until this clears.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-autoscaler'
          summary: '{{ $labels.namespace }}: cluster autoscaler scale-up attempts are failing'
          title: '{{ $labels.namespace }}: cluster autoscaler scale-up attempts are failing'
        }
        expression: 'errors:cluster_autoscaler:failed_scale_ups:rate15m > 0'
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
        alert: 'UserJourneyClusterAutoscalerScaleUpStuck'
        enabled: true
        labels: {
          component: 'slo'
          severity: '3'
          slo: 'cluster-autoscaler-scaling'
        }
        annotations: {
          correlationId: 'UserJourneyClusterAutoscalerScaleUpStuck/{{ $labels.cluster }}/{{ $labels.namespace }}'
          description: 'For over 20 minutes the cluster autoscaler for hosted cluster {{ $labels.namespace }} has had unschedulable pods while still below its maximum node count (headroom available). The autoscaler is allowed to add nodes and demand exists, but the pods are not being scheduled — the scale-up operation is blocked (e.g. nodes failing to join, provisioning stuck). This is distinct from a cluster at its configured max, which is not alerted. SRE investigation needed.'
          info: 'For over 20 minutes the cluster autoscaler for hosted cluster {{ $labels.namespace }} has had unschedulable pods while still below its maximum node count (headroom available). The autoscaler is allowed to add nodes and demand exists, but the pods are not being scheduled — the scale-up operation is blocked (e.g. nodes failing to join, provisioning stuck). This is distinct from a cluster at its configured max, which is not alerted. SRE investigation needed.'
          runbook_url: 'https://aka.ms/arohcp-runbook-cluster-autoscaler'
          summary: '{{ $labels.namespace }}: cluster autoscaler scale-up appears stuck (pending pods with headroom)'
          title: '{{ $labels.namespace }}: cluster autoscaler scale-up appears stuck (pending pods with headroom)'
        }
        expression: '(pods:cluster_autoscaler:unschedulable_count > 0) and on (cluster, namespace) (nodes:cluster_autoscaler:ready_count < nodes:cluster_autoscaler:max_count)'
        for: 'PT20M'
        severity: severityCeiling > 0 ? max(3, severityCeiling) : 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
