@description('The resource ID to assign the role on')
param resourceId string

@description('The Grafana managed identity principal ID')
param grafanaPrincipalId string

@description('The role definition ID (GUID)')
param roleDefinitionId string

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceId, grafanaPrincipalId, roleDefinitionId)
  properties: {
    principalId: grafanaPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', roleDefinitionId)
  }
}


