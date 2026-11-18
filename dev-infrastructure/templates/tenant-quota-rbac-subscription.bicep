targetScope = 'subscription'

@description('Principal ID of the tenant-quota collector service principal')
param collectorPrincipalId string

var readerRole = 'acdd72a7-3385-48ef-bd42-f606fba81ae7'

resource readerAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, collectorPrincipalId, readerRole)
  scope: subscription()
  properties: {
    principalId: collectorPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', readerRole)
    description: 'Tenant-quota collector: read subscription quota and role assignment counts'
  }
}
