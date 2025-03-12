param location string

@description('Metrics global Grafana name')
param grafanaName string

@description('The admin group principal ID to manage Grafana')
param grafanaAdminGroupPrincipalId string

@description('The identity that will manage Grafana')
param grafanaManagerPrincipalId string

@description('The zone redundant mode of Grafana')
param zoneRedundancy bool

@description('The azure monitor workspace IDs to integrate with Grafana')
param azureMonitorWorkspaceIds array

// Azure Managed Grafana Workspace Contributor: Can manage Azure Managed Grafana resources, without providing access to the workspaces themselves.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#azure-managed-grafana-workspace-contributor
var grafanaContributor = '5c2d7e57-b7c2-4d8a-be4f-82afa42c6e95'

// Grafana Admin: Perform all Grafana operations, including the ability to manage data sources, create dashboards, and manage role assignments within Grafana.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#grafana-admin
var grafanaAdminRole = '22926164-76b3-42b3-bc55-97df8dab3e41'

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' = {
  name: grafanaName
  location: location
  sku: {
    name: 'Standard'
  }
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    grafanaIntegrations: {
      azureMonitorWorkspaceIntegrations: [
        for workspaceId in azureMonitorWorkspaceIds: {
          azureMonitorWorkspaceResourceId: workspaceId
        }
      ]
    }
    zoneRedundancy: zoneRedundancy ? 'Enabled' : 'Disabled'
  }
}

resource grafanaManagerContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafana.id, grafanaManagerPrincipalId, grafanaContributor)
  scope: grafana
  properties: {
    principalId: grafanaManagerPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaContributor)
  }
}

resource grafanaManagerAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafana.id, grafanaManagerPrincipalId, grafanaAdminRole)
  scope: grafana
  properties: {
    principalId: grafanaManagerPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaAdminRole)
  }
}

resource adminRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafana.id, grafanaAdminGroupPrincipalId, grafanaAdminRole)
  scope: grafana
  properties: {
    principalId: grafanaAdminGroupPrincipalId
    principalType: 'group'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaAdminRole)
  }
}

output grafanaId string = grafana.id
