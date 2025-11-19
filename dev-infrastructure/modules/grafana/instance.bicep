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

resource grafana 'Microsoft.Dashboard/grafana@2025-08-01' = {
  name: grafanaName
  location: location
  tags: {
    // To enable AMG cross-tenant login, you can add a tag to the AMG resource in the Secure tenant.
    // Name: AMG.CrossTenant.SecurityGroup
    // Value: GroupObjectId;TenantId (eg.31e30f0f-d56f-422b-816a-24fb8fb11fe8;72f988bf-86f1-41af-91ab-2d7cd011db47)
    // The tag references a security group in CORP tenant which can access the AMG in the secure tenant.
    // The GroupObjectId is the object id of a security group in the CORP tenant. The TenantId is CORP tenant id.
    // For each AMG instance, only one security group is supported for cross-tenant access.
    // The user which is member of the cross-tenant security group is able to access the AMG in the secure tenant, user is assigned with Grafana Viewer role when accessing AMG from a different tenant.
    'AMG.CrossTenant.SecurityGroup': 'f39df48c-d658-4a0c-95d3-a331e7acaf29;72f988bf-86f1-41af-91ab-2d7cd011db47' // TM-ARO-Engineering Group ObjectId
  }
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
