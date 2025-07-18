#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param allSev1ActionGroups array

#disable-next-line no-unused-params
param allSev2ActionGroups array

#disable-next-line no-unused-params
param allSev3ActionGroups array

#disable-next-line no-unused-params
param allSev4ActionGroups array

resource clusterServiceSlos 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'cluster-service-slos'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        actions: [for g in allSev2ActionGroups: { actionGroupId: g }]
        alert: 'ClustersServiceAPIAvailability5mto1hor30mto6hErrorBudgetBurn'
        enabled: true
        labels: {
          long: '6h'
          severity: 'critical'
          short: '30m'
        }
        annotations: {
          description: 'API is rapidly burning its 28 day availability error budget (99% SLO)'
          runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn'
          summary: 'Cluster Service API error budget burn rate is too high'
        }
        expression: '( sum(max without(prometheus_replica) (availability:api_inbound_request_count:burnrate5m{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1-0.99000))    and  sum(max without(prometheus_replica) (availability:api_inbound_request_count:burnrate1h{namespace="clusters-service", service="clusters-service-metrics"})) > (13.44 * (1-0.99000)) ) or ( sum(max without(prometheus_replica) (availability:api_inbound_request_count:burnrate30m{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1-0.99000))    and  sum(max without(prometheus_replica) (availability:api_inbound_request_count:burnrate6h{namespace="clusters-service", service="clusters-service-metrics"})) > (5.6 * (1-0.99000)) )'
        for: 'PT5M'
        severity: 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
