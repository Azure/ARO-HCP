targetScope = 'subscription'

@description('The principal ID of the managed identity that needs Reader permissions')
param managedIdentityPrincipalId string

@description('Environment name for naming consistency')
param environment string = 'test'

@description('Name prefix for naming consistency')
param namePrefix string = 'service-tags'

// Reader role assignment at subscription level for IP discovery
resource subscriptionReaderRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, managedIdentityPrincipalId, 'Reader')
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'acdd72a7-3385-48ef-bd42-f606fba81ae7') // Reader
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

output subscriptionId string = subscription().subscriptionId
output subscriptionName string = subscription().displayName
output roleAssignmentId string = subscriptionReaderRole.id