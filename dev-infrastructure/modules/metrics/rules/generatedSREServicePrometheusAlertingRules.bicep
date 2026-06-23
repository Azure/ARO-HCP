#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

#disable-next-line no-unused-params
param location string = resourceGroup().location

resource frontendLatency 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'frontend-latency'
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
        alert: 'FrontendLatency'
        enabled: true
        labels: {
          severity: 'info'
        }
        annotations: {
          correlationId: 'FrontendLatency/{{ $labels.cluster }}'
          description: 'The 95th percentile of frontend request latency has exceeded 5 seconds over the past hour.'
          info: 'The 95th percentile of frontend request latency has exceeded 5 seconds over the past hour.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/frontend-tsg.html'
          summary: 'Frontend latency is high: 95th percentile exceeds 5 seconds'
          title: 'Frontend latency is high: 95th percentile exceeds 5 seconds'
        }
        expression: 'histogram_quantile(0.95, rate(frontend_http_requests_duration_seconds_bucket[1h])) > 5'
        for: 'PT15M'
        severity: 4
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}

resource backendRetryhotloop 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'backend-retryhotloop'
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
        alert: 'BackendControllerRetryHotLoop'
        enabled: true
        labels: {
          severity: 'warning'
        }
        annotations: {
          correlationId: 'BackendControllerRetryHotLoop/{{ $labels.cluster }}/{{ $labels.name }}'
          description: 'Backend controller workqueue {{ $labels.name }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          info: 'Backend controller workqueue {{ $labels.name }} has a retry ratio of > 50% sustained over 10 minutes, indicating most queue activity is failed retries rather than fresh work.'
          runbook_url: 'https://eng.ms/docs/cloud-ai-platform/azure-core/azure-cloud-native-and-management-platform/control-plane-bburns/azure-red-hat-openshift/azure-redhat-openshift-team-doc/hcp/troubleshooting/backend-tsg.html'
          summary: 'Backend controller workqueue {{ $labels.name }} retry hot loop'
          title: 'Backend controller workqueue {{ $labels.name }} retry hot loop'
        }
        expression: '( sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_retries_total{namespace="aro-hcp"}[10m]) ) ) / sum by (name, cluster) ( max without(prometheus_replica) ( rate(workqueue_adds_total{namespace="aro-hcp"}[10m]) ) ) ) > 0.5'
        for: 'PT10M'
        severity: 3
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
