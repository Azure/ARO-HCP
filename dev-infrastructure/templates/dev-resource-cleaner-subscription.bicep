targetScope = 'subscription'

@description('Principal ID of the resource cleaner managed identity')
param principalId string

var contributorRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)

resource contributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, principalId, contributorRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: contributorRoleDefinitionId
  }
}
