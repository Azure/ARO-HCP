@description('The principal id of the service principal that will be assigned access to the ACR')
param principalId string

@description('Whether to grant push access to the ACR')
param grantPushAccess bool = false

var acrPullRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

var acrPushRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '8311e382-0749-4cb8-b61a-304f252e45ec'
)

resource acrPullRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if(!grantPushAccess) {
  name: deployment().name
  properties: {
    principalId: principalId
    roleDefinitionId: acrPullRoleDefinitionId
    principalType: 'ServicePrincipal'
  }
}

resource acrPushRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if(grantPushAccess)  {
  name: deployment().name
  properties: {
    principalId: principalId
    roleDefinitionId: acrPushRoleDefinitionId
    principalType: 'ServicePrincipal'
  }
}
