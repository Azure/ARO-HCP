import {
  splitOrEmptyArray
} from '../common.bicep'

param location string

@description('Metrics global Grafana name')
param grafanaName string

@description('The Grafana major version')
param grafanaMajorVersion string

@description('The identity that will manage Grafana')
param grafanaManagerPrincipalId string

@description('The zone redundant mode of Grafana')
param zoneRedundancy bool

@description('The azure monitor workspace IDs to integrate with Grafana')
param azureMonitorWorkspaceIds array

@description('List of grafana role assignments as a space-separated list of items in the format of "principalId/principalType/role"')
param grafanaRoles string
var grafanaRolesArray = [
  for gr in splitOrEmptyArray(grafanaRoles, ' '): {
    principalId: split(gr, '/')[0]
    principalType: split(gr, '/')[1]
    role: split(gr, '/')[2]
  }
]

resource grafana 'Microsoft.Dashboard/grafana@2024-10-01' = {
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
    grafanaMajorVersion: grafanaMajorVersion
  }
}

// Built-in roles for Azure Monitor Grafana
var grafanaBuiltInRoles = {
  Contributor: '5c2d7e57-b7c2-4d8a-be4f-82afa42c6e95'
  Admin: '22926164-76b3-42b3-bc55-97df8dab3e41'
  Viewer: '60921a7e-fef1-4a43-9b16-a26c52ad4769'
}

resource grafanaManagerAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(grafana.id, grafanaManagerPrincipalId, grafanaBuiltInRoles.Admin)
  scope: grafana
  properties: {
    principalId: grafanaManagerPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaBuiltInRoles.Admin)
  }
}

resource grafanaRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for gra in grafanaRolesArray: {
    name: guid(grafana.id, gra.principalId, gra.role)
    scope: grafana
    properties: {
      principalId: gra.principalId
      principalType: gra.principalType
      roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaBuiltInRoles[gra.role])
    }
  }
]

output grafanaId string = grafana.id
