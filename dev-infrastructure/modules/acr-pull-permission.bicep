param principalId string

var acrPullRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

resource acrPullRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: deployment().name
  properties: {
    principalId: principalId
    roleDefinitionId: acrPullRoleDefinitionId
    principalType: 'ServicePrincipal'
  }
}
