targetScope = 'subscription'

param globalMSIId string

var contributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)

var keyVaultPurgeOperator = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'a68e7c17-0ab2-4c09-9a58-125dae29748c'
)

resource aroDevopsMSIResourceGroupContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, globalMSIId, contributorRoleId)
  properties: {
    principalId: reference(globalMSIId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: contributorRoleId
  }
}

resource aroDevopsMSIResourceGroupKeyVaultPurgeOperator 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, globalMSIId, keyVaultPurgeOperator)
  properties: {
    principalId: reference(globalMSIId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: keyVaultPurgeOperator
  }
}
