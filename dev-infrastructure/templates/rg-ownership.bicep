param aroDevopsMsiId string

var contributorRoleId = 'b24988ac-6180-42a0-ab88-20f7382dd24c'

resource aroDevopsMSIResourceGroupOwner 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, aroDevopsMsiId, contributorRoleId)
  scope: resourceGroup()
  properties: {
    principalId: reference(aroDevopsMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: contributorRoleId
  }
}
