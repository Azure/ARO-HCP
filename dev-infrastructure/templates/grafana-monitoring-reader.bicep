targetScope = 'subscription'

@description('The principal ID of the global Grafana managed identity')
param grafanaPrincipalId string

// Monitoring Reader lets the Grafana managed identity read Azure Monitor
// platform metrics (the built-in azure-monitor-oob datasource) for every
// resource in this subscription. Granting at subscription scope avoids a
// per-resource role assignment for each new metric source.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#monitoring-reader
var monitoringReaderRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '43d0d8ad-25c7-4714-9337-8ba259a9fe05'
)

resource grafanaMonitoringReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, grafanaPrincipalId, monitoringReaderRoleId)
  scope: subscription()
  properties: {
    principalId: grafanaPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: monitoringReaderRoleId
  }
}
