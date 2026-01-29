#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

resource InstancesDownV1 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'InstancesDownV1'
  location: resourceGroup().location
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
        alert: 'InstancesDownV1'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          correlationId: 'InstancesDownV1/{{ $labels.cluster }}'
          description: 'All instances of the App are down'
          info: 'All instances of the App are down'
          summary: 'All instances of the App are down'
          title: 'All instances of the App are down'
        }
        expression: 'sum(up{job="app"}) == 0'
        severity: 3
      }
      {
        actions: [for g in actionGroups: {
          actionGroupId: g
          actionProperties: {
            'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
            'IcM.CorrelationId': '#$.annotations.correlationId#'
          }
        }]
        alert: 'KubePodNotReady'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'KubePodNotReady/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'
          description: 'Pod {{ $labels.namespace }}/{{ $labels.pod}} has been in a non-ready state for longer than 15 minutes.'
          info: 'Pod {{ $labels.namespace }}/{{ $labels.pod}} has been in a non-ready state for longer than 15 minutes.'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodnotready'
          summary: 'Pod has been in a non-ready state for more than 15 minutes.'
          title: 'Pod has been in a non-ready state for more than 15 minutes.'
        }
        expression: 'sum by (namespace, pod, cluster) ( max by(namespace, pod, cluster) ( kube_pod_status_phase{job="kube-state-metrics", phase=~"Pending|Unknown|Failed"} ) * on(namespace, pod, cluster) group_left(owner_kind) topk by(namespace, pod, cluster) ( 1, max by(namespace, pod, owner_kind, cluster) (kube_pod_owner{owner_kind!="Job"}) ) ) > 0'
        for: 'PT15M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
