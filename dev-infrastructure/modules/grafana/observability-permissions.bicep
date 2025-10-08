@description('The Grafana resource ID')
param grafanaResourceId string

@description('The Log Analytics workspace resource ID (optional)')
param logAnalyticsWorkspaceId string = ''

@description('The Azure Front Door profile resource ID (optional)')
param frontDoorProfileId string = ''

import * as res from '../resource.bicep'

var grafanaRef = res.grafanaRefFromId(grafanaResourceId)

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = {
  name: grafanaRef.name
  scope: resourceGroup(grafanaRef.resourceGroup.subscriptionId, grafanaRef.resourceGroup.name)
}

// Azure built-in role definition IDs - these are global constants across all Azure environments
// They are the same in every subscription, tenant, and cloud (public, gov, etc.)
// See: https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles

// Log Analytics Reader role - allows reading data from Log Analytics workspaces
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#log-analytics-reader
var logAnalyticsReader = '73c42c96-874c-492b-b04d-ab87d138a893'

// Monitoring Reader role - allows reading monitoring data (metrics, logs, alerts)
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#monitoring-reader
var monitoringReader = '43d0d8ad-25c7-4714-9337-8ba259a9fe05'

// Grant Grafana managed identity access to Log Analytics workspace
// This allows Grafana to query logs and metrics from the workspace
module logAnalyticsRoleAssignment './observability-role-assignment.bicep' = if (logAnalyticsWorkspaceId != '') {
  name: 'grafana-loganalytics-role'
  scope: resourceGroup(split(logAnalyticsWorkspaceId, '/')[2], split(logAnalyticsWorkspaceId, '/')[4])
  params: {
    resourceId: logAnalyticsWorkspaceId
    grafanaResourceId: grafanaResourceId
    grafanaPrincipalId: grafana.identity.principalId
    roleDefinitionId: logAnalyticsReader
  }
}

// Grant Grafana managed identity access to Azure Front Door metrics
// This allows Grafana to query AFD platform metrics directly from Azure Monitor
module frontDoorRoleAssignment './observability-role-assignment.bicep' = if (frontDoorProfileId != '') {
  name: 'grafana-frontdoor-role'
  scope: resourceGroup(split(frontDoorProfileId, '/')[2], split(frontDoorProfileId, '/')[4])
  params: {
    resourceId: frontDoorProfileId
    grafanaResourceId: grafanaResourceId
    grafanaPrincipalId: grafana.identity.principalId
    roleDefinitionId: monitoringReader
  }
}
