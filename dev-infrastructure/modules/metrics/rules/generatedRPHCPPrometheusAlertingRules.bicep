#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource hcpUpgradeNewAlert 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'hcp-upgrade-new-alert'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [for g in actionGroups: {
          actionGroupId: g
          actionProperties: {
            'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
            'IcM.CorrelationId': '#$.annotations.correlationId#'
          }
        }]
        alert: 'HCPUpgradeNewAlert'
        enabled: true
        labels: {
          component: 'hcp'
          hcp_upgrade_new_alert: 'true'
          severity: '4'
        }
        annotations: {
          correlationId: 'HCPUpgradeNewAlert/{{ $labels.resource_id }}/{{ $labels.version }}/{{ $labels.attempt_started_at }}'
          description: 'HCP {{ $labels.resource_id }} has a firing namespace-level alert type activated at or after the upgrade to {{ $labels.version }} started at {{ $labels.attempt_started_at }}, with no observed firing in the 24 hours before that upgrade started. The upgrade completed within 24 hours and lasted at most 24 hours. Timing is evidence for investigation, not proof of causation.'
          info: 'HCP {{ $labels.resource_id }} has a firing namespace-level alert type activated at or after the upgrade to {{ $labels.version }} started at {{ $labels.attempt_started_at }}, with no observed firing in the 24 hours before that upgrade started. The upgrade completed within 24 hours and lasted at most 24 hours. Timing is evidence for investigation, not proof of causation.'
          summary: '{{ $labels.resource_id }}: new control-plane alert after upgrade to {{ $labels.version }} ({{ $labels.attempt_started_at }})'
          title: '{{ $labels.resource_id }}: new control-plane alert after upgrade to {{ $labels.version }} ({{ $labels.attempt_started_at }})'
        }
        expression: 'max by (resource_id, version, attempt_started_at, region) (((max by (namespace, alertname, resource_id, region) (hcp:alerts_for_state and ignoring (alertstate) hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) >= on (resource_id) group_left (version, attempt_started_at) topk by (resource_id, region) (1, hcp:upgrade_started_timestamp_seconds)) unless on (namespace, alertname, resource_id, attempt_started_at) max by (namespace, alertname, resource_id, attempt_started_at, region) (max_over_time(((timestamp(hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) < on (resource_id) group_left (attempt_started_at) topk by (resource_id, region) (1, hcp:upgrade_started_timestamp_seconds @ end())) >= on (resource_id, attempt_started_at) group_left () (topk by (resource_id, region) (1, hcp:upgrade_started_timestamp_seconds @ end()) - 86400))[3d:1m]))) or ((max by (namespace, alertname, resource_id, region) (hcp:alerts_for_state and ignoring (alertstate) hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) >= on (resource_id) group_left (version, attempt_started_at) (topk by (resource_id, region) (2, hcp:upgrade_started_timestamp_seconds) unless topk by (resource_id, region) (1, hcp:upgrade_started_timestamp_seconds))) unless on (namespace, alertname, resource_id, attempt_started_at) max by (namespace, alertname, resource_id, attempt_started_at, region) (max_over_time(((timestamp(hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) < on (resource_id) group_left (attempt_started_at) (topk by (resource_id, region) (2, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (1, hcp:upgrade_started_timestamp_seconds @ end()))) >= on (resource_id, attempt_started_at) group_left () ((topk by (resource_id, region) (2, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (1, hcp:upgrade_started_timestamp_seconds @ end())) - 86400))[3d:1m]))) or ((max by (namespace, alertname, resource_id, region) (hcp:alerts_for_state and ignoring (alertstate) hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) >= on (resource_id) group_left (version, attempt_started_at) (topk by (resource_id, region) (3, hcp:upgrade_started_timestamp_seconds) unless topk by (resource_id, region) (2, hcp:upgrade_started_timestamp_seconds))) unless on (namespace, alertname, resource_id, attempt_started_at) max by (namespace, alertname, resource_id, attempt_started_at, region) (max_over_time(((timestamp(hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) < on (resource_id) group_left (attempt_started_at) (topk by (resource_id, region) (3, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (2, hcp:upgrade_started_timestamp_seconds @ end()))) >= on (resource_id, attempt_started_at) group_left () ((topk by (resource_id, region) (3, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (2, hcp:upgrade_started_timestamp_seconds @ end())) - 86400))[3d:1m]))) or ((max by (namespace, alertname, resource_id, region) (hcp:alerts_for_state and ignoring (alertstate) hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) >= on (resource_id) group_left (version, attempt_started_at) (topk by (resource_id, region) (4, hcp:upgrade_started_timestamp_seconds) unless topk by (resource_id, region) (3, hcp:upgrade_started_timestamp_seconds))) unless on (namespace, alertname, resource_id, attempt_started_at) max by (namespace, alertname, resource_id, attempt_started_at, region) (max_over_time(((timestamp(hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) < on (resource_id) group_left (attempt_started_at) (topk by (resource_id, region) (4, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (3, hcp:upgrade_started_timestamp_seconds @ end()))) >= on (resource_id, attempt_started_at) group_left () ((topk by (resource_id, region) (4, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (3, hcp:upgrade_started_timestamp_seconds @ end())) - 86400))[3d:1m]))) or ((max by (namespace, alertname, resource_id, region) (hcp:alerts_for_state and ignoring (alertstate) hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) >= on (resource_id) group_left (version, attempt_started_at) (topk by (resource_id, region) (5, hcp:upgrade_started_timestamp_seconds) unless topk by (resource_id, region) (4, hcp:upgrade_started_timestamp_seconds))) unless on (namespace, alertname, resource_id, attempt_started_at) max by (namespace, alertname, resource_id, attempt_started_at, region) (max_over_time(((timestamp(hcp:alerts{alertstate="firing",hcp_upgrade_new_alert!="true",namespace!="",resource_id!=""}) < on (resource_id) group_left (attempt_started_at) (topk by (resource_id, region) (5, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (4, hcp:upgrade_started_timestamp_seconds @ end()))) >= on (resource_id, attempt_started_at) group_left () ((topk by (resource_id, region) (5, hcp:upgrade_started_timestamp_seconds @ end()) unless topk by (resource_id, region) (4, hcp:upgrade_started_timestamp_seconds @ end())) - 86400))[3d:1m]))))'
        severity: severityCeiling > 0 ? max(4, severityCeiling) : 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource rpUserjourneyKasAvailabilityMonitorRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'rp-userjourney-kas-availability-monitor-rules'
  location: location
  properties: {
    interval: 'PT1M'
    rules: [
      {
        actions: [for g in actionGroups: {
          actionGroupId: g
          actionProperties: {
            'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
            'IcM.CorrelationId': '#$.annotations.correlationId#'
          }
        }]
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
        actions: [for g in actionGroups: {
          actionGroupId: g
          actionProperties: {
            'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
            'IcM.CorrelationId': '#$.annotations.correlationId#'
          }
        }]
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
        actions: [for g in actionGroups: {
          actionGroupId: g
          actionProperties: {
            'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
            'IcM.CorrelationId': '#$.annotations.correlationId#'
          }
        }]
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
        actions: [for g in actionGroups: {
          actionGroupId: g
          actionProperties: {
            'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
            'IcM.CorrelationId': '#$.annotations.correlationId#'
          }
        }]
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
