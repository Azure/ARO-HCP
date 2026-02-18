targetScope = 'subscription'

@description('The principal ID of the Exporter managed identity')
param exporterPrincipalId string

var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

resource aroHcpExporterReaderSvc 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, exporterPrincipalId, readerRoleId)
  scope: subscription()
  properties: {
    principalId: exporterPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}
