targetScope = 'subscription'

// Least-privilege custom role for the Admin API to manage alert processing
// rules (Microsoft.AlertsManagement/actionRules) used for Geneva
// Action-invoked alert suppression.
var alertProcessingRuleRoleName = 'ARO HCP Alert Processing Rule Operator-${subscription().subscriptionId}'

resource alertProcessingRuleRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, 'AdminApiAlertProcessingRuleOperator')
  properties: {
    roleName: alertProcessingRuleRoleName
    description: 'Allows managing Microsoft.AlertsManagement/actionRules.'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.AlertsManagement/actionRules/*'
          'Microsoft.Monitor/accounts/read'
        ]
        notActions: []
        dataActions: []
        notDataActions: []
      }
    ]
    assignableScopes: [
      subscription().id
    ]
  }
}
