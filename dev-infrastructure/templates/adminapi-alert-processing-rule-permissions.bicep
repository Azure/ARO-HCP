@description('Principal ID of the Admin API managed identity')
param adminApiPrincipalId string

var alertProcessingRuleRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  guid(subscription().id, 'AdminApiAlertProcessingRuleOperator')
)

resource alertProcessingRuleRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: resourceGroup()
  name: guid(resourceGroup().id, adminApiPrincipalId, alertProcessingRuleRoleId)
  properties: {
    roleDefinitionId: alertProcessingRuleRoleId
    principalId: adminApiPrincipalId
    principalType: 'ServicePrincipal'
  }
}
