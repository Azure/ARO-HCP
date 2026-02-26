@description('The Grafana managed identity principal ID')
param grafanaPrincipalId string

@description('The Azure Front Door profile resource ID (optional)')
param frontDoorProfileId string = ''

// Azure built-in role definition IDs - these are global constants across all Azure environments
// They are the same in every subscription, tenant, and cloud (public, gov, etc.)
// See: https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles

// Monitoring Reader role - allows reading monitoring data (metrics, logs, alerts)
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#monitoring-reader
var monitoringReader = '43d0d8ad-25c7-4714-9337-8ba259a9fe05'

// Grant Grafana managed identity access to Azure Front Door metrics
// This allows Grafana to query AFD platform metrics directly from Azure Monitor
module frontDoorRoleAssignment './observability-role-assignment.bicep' = if (frontDoorProfileId != '') {
  name: 'grafana-afd-role-${uniqueString(frontDoorProfileId, grafanaPrincipalId, monitoringReader)}'
  scope: resourceGroup(split(frontDoorProfileId, '/')[2], split(frontDoorProfileId, '/')[4])
  params: {
    resourceId: frontDoorProfileId
    grafanaPrincipalId: grafanaPrincipalId
    roleDefinitionId: monitoringReader
  }
}
