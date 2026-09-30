@description('Principal ID of the Admin API managed identity')
param adminApiPrincipalId string

// Monitoring Contributor role
var monitoringContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '749f88d5-cbae-40b8-bcfc-e573ddc772fa'
)

resource monitoringContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(resourceGroup().id, adminApiPrincipalId, monitoringContributorRoleId)
  properties: {
    roleDefinitionId: monitoringContributorRoleId
    principalId: adminApiPrincipalId
    principalType: 'ServicePrincipal'
  }
}
