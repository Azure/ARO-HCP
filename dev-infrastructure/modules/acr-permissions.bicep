@description('The principal id of the service principal that will be assigned access to the ACR')
param principalId string

@description('Whether to grant push access to the ACR')
param grantPushAccess bool = false

@description('Whether to grant contributor access to the ACR')
param grantContributorAccess bool = false

@description('ACR Namespace Resource Group Name')
param acrResourceGroupid string

var acrPullRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

var acrPushRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '8311e382-0749-4cb8-b61a-304f252e45ec'
)

var acrDeleteRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'c2f4ef07-c644-48eb-af81-4b1b4947fb11'
)

var contributorRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)

resource acrPullRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!grantPushAccess) {
  name: guid(acrResourceGroupid, principalId, acrPullRoleDefinitionId)
  properties: {
    principalId: principalId
    roleDefinitionId: acrPullRoleDefinitionId
    principalType: 'ServicePrincipal'
  }
}

resource acrPushRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (grantPushAccess) {
  name: guid(acrResourceGroupid, principalId, acrPushRoleDefinitionId)
  properties: {
    principalId: principalId
    roleDefinitionId: acrPushRoleDefinitionId
    principalType: 'ServicePrincipal'
  }
}

resource acrDeleteRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (grantPushAccess) {
  name: guid(acrResourceGroupid, principalId, acrDeleteRoleDefinitionId)
  properties: {
    principalId: principalId
    roleDefinitionId: acrDeleteRoleDefinitionId
    principalType: 'ServicePrincipal'
  }
}

resource acrContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (grantContributorAccess) {
  name: guid(acrResourceGroupid, principalId, contributorRoleDefinitionId)
  properties: {
    roleDefinitionId: contributorRoleDefinitionId
    principalId: principalId
    principalType: 'ServicePrincipal'
  }
}
