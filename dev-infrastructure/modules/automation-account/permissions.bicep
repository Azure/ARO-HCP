targetScope = 'subscription'

@description('Name of the automation account')
param automationAccountName string

@description('Name of the managed identity')
param automationAccountManagedIdentityId string

var contributorRole = 'b24988ac-6180-42a0-ab88-20f7382dd24c'
var userAccessAdminRole = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

var roleAssignmentsToCreate = [
  {
    roleId: contributorRole
    scope: subscription()
  }
  {
    roleId: userAccessAdminRole
    scope: subscription()
  }
]
resource roleAssignments 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for ra in roleAssignmentsToCreate: {
    name: guid(automationAccountName, automationAccountManagedIdentityId, ra.roleId)
    scope: ra.scope
    properties: {
      principalId: automationAccountManagedIdentityId
      principalType: 'ServicePrincipal'
      roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', ra.roleId)
    }
  }
]
