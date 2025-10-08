@description('The resource ID to assign the role on')
param resourceId string

@description('The Grafana resource ID (for GUID generation)')
param grafanaResourceId string

@description('The Grafana managed identity principal ID')
param grafanaPrincipalId string

@description('The role definition ID (GUID)')
param roleDefinitionId string

// Parse the resource type from the resource ID
var resourceType = '${split(resourceId, '/')[6]}/${split(resourceId, '/')[7]}'
var resourceName = last(split(resourceId, '/'))

// Use a generic existing resource reference
// This works for any resource type since we're just using it as a scope
resource targetResource 'Microsoft.Resources/resourceGroups@2021-04-01' existing = {
  scope: subscription()
  name: resourceGroup().name
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceId, grafanaResourceId, roleDefinitionId)
  properties: {
    principalId: grafanaPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', roleDefinitionId)
  }
}

