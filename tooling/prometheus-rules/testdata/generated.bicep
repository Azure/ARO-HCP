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

resource InstancesDownV1 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'InstancesDownV1'
  location: resourceGroup().location
  properties: {
    rules: [
      {
        actions: [for g in allSev2ActionGroups: { actionGroupId: g }]
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
