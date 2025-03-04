targetScope = 'subscription'

@description('Name of the automation account')
param automationAccountName string

@description('Name of the managed identity')
param automationAccountManagedIdentityId string

var contributorRole = 'b24988ac-6180-42a0-ab88-20f7382dd24c'

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(automationAccountName, automationAccountManagedIdentityId, contributorRole)
  properties: {
    principalId: automationAccountManagedIdentityId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', contributorRole)
  }
}
