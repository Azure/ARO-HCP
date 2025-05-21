param azureMonitoring string

resource InstancesDownV1 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'InstancesDownV1'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        alert: 'InstancesDownV1'
        enabled: true
        labels: {
          severity: 'critical'
        }
        annotations: {
          description: 'All instances of the App are down'
          summary: 'All instances of the App are down'
        }
        expression: 'sum(up{job="app"}) == 0'
        severity: 2
      }
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
