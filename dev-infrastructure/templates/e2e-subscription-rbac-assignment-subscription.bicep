targetScope = 'subscription'

@description('Subscription ID that owns the shared custom role definitions')
param homeSubscriptionId string

@description('Principal ID for aro-dev-first-party2')
param firstPartyPrincipalId string

@description('Principal ID for aro-dev-arm-helper2')
param armHelperPrincipalId string

@description('Principal ID for aro-dev-msi-mock2')
param miMockPrincipalId string

@description('Pooled MSI mock principals that also need customer-subscription access')
param msiMockPoolPrincipals array = []

@description('Custom role name for the first-party mock principal')
param firstPartyRoleName string = 'dev-first-party-mock'

@description('Custom role name for the MSI mock principal')
param msiMockRoleName string = 'dev-msi-mock'

@description('Historical custom role name for the KMS plugin role')
param kmsPluginRoleName string = 'Azure Red Hat OpenShift KMS Plugin - Dev'

var homeSubscriptionScope = '/subscriptions/${homeSubscriptionId}'
var contributorRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)
var rbacAdminRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'f58310d9-a9f6-439a-9e8d-f62e7b41a168'
)
var firstPartyRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  guid(homeSubscriptionScope, firstPartyRoleName)
)
var msiMockRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  guid(homeSubscriptionScope, msiMockRoleName)
)
var kmsPluginRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  guid(kmsPluginRoleName)
)
resource firstPartyRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, firstPartyPrincipalId, firstPartyRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: firstPartyPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: firstPartyRoleDefinitionId
  }
}

resource armHelperContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, armHelperPrincipalId, contributorRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: armHelperPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: contributorRoleDefinitionId
  }
}

resource armHelperRbacAdminRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, armHelperPrincipalId, rbacAdminRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: armHelperPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: rbacAdminRoleDefinitionId
  }
}

resource miMockRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, miMockPrincipalId, msiMockRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: miMockPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: msiMockRoleDefinitionId
  }
}

resource miMockKmsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, miMockPrincipalId, kmsPluginRoleDefinitionId)
  scope: subscription()
  properties: {
    principalId: miMockPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: kmsPluginRoleDefinitionId
  }
}

resource pooledMiMockRoleAssignments 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for principal in msiMockPoolPrincipals: {
    name: guid(subscription().id, principal.principalId, msiMockRoleDefinitionId)
    scope: subscription()
    properties: {
      principalId: principal.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: msiMockRoleDefinitionId
    }
  }
]

resource pooledMiMockKmsRoleAssignments 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for principal in msiMockPoolPrincipals: {
    name: guid(subscription().id, principal.principalId, kmsPluginRoleDefinitionId)
    scope: subscription()
    properties: {
      principalId: principal.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: kmsPluginRoleDefinitionId
    }
  }
]
