targetScope = 'subscription'

@description('Principal ID of the CI bot service principal')
param botPrincipalId string

@description('Whether this subscription hosts global infrastructure (extra data-plane roles)')
param isGlobalSubscription bool = false

@description('Whether to grant AKS RBAC Cluster Admin (only needed in DEV for NSG rule management)')
param grantAksRbac bool = false

var contributorRole = 'b24988ac-6180-42a0-ab88-20f7382dd24c'
var rbacAdminRole = 'f58310d9-a9f6-439a-9e8d-f62e7b41a168'
var aksRbacClusterAdminRole = 'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'

var ownerRole = '8e3af657-a8ff-443c-a75c-2fe8c4bcb635'
var userAccessAdminRole = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

var keyVaultAdminRole = '00482a5a-887f-4fb3-b363-3b7fe8e74483'
var grafanaAdminRole = '22926164-76b3-42b3-bc55-97df8dab3e41'

resource contributorAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, botPrincipalId, contributorRole)
  scope: subscription()
  properties: {
    principalId: botPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', contributorRole)
  }
}

resource rbacAdminAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, botPrincipalId, rbacAdminRole)
  scope: subscription()
  properties: {
    principalId: botPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', rbacAdminRole)
    condition: '((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/write\'})) OR (@Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${ownerRole}, ${userAccessAdminRole}, ${rbacAdminRole}})) AND ((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/delete\'})) OR (@Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${ownerRole}, ${userAccessAdminRole}, ${rbacAdminRole}}))'
    conditionVersion: '2.0'
    description: 'CI bot: assign all roles except Owner, UAA, RBAC Administrator'
  }
}

resource aksRbacClusterAdminAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (grantAksRbac) {
  name: guid(subscription().id, botPrincipalId, aksRbacClusterAdminRole)
  scope: subscription()
  properties: {
    principalId: botPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', aksRbacClusterAdminRole)
  }
}

resource keyVaultAdminAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (isGlobalSubscription) {
  name: guid(subscription().id, botPrincipalId, keyVaultAdminRole)
  scope: subscription()
  properties: {
    principalId: botPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', keyVaultAdminRole)
  }
}

resource grafanaAdminAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (isGlobalSubscription) {
  name: guid(subscription().id, botPrincipalId, grafanaAdminRole)
  scope: subscription()
  properties: {
    principalId: botPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaAdminRole)
  }
}
