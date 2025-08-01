targetScope = 'subscription'

@description('Name of the automation account')
param automationAccountName string

@description('Name of the managed identity')
param principalId string

var contributorRole = 'b24988ac-6180-42a0-ab88-20f7382dd24c'
var userAccessAdminRole = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

var roleAssignmentsToCreate = [
  {
    roleId: contributorRole
  }
  {
    roleId: userAccessAdminRole
  }
]
resource roleAssignments 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for ra in roleAssignmentsToCreate: {
    name: guid(automationAccountName, principalId, ra.roleId)
    scope: subscription()
    properties: {
      principalId: principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', ra.roleId)
    }
  }
]
