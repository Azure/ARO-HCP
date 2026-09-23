targetScope = 'subscription'

import { restrictedRoleAssignmentCondition, restrictedRoleAssignmentConditionVersion } from '../modules/common.bicep'

@description('The principal ID of the Prow OpenShift Release Bot')
param prowPrincipalId string

var contributorRole = 'b24988ac-6180-42a0-ab88-20f7382dd24c'
var userAccessAdminRole = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

resource contributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, prowPrincipalId, contributorRole)
  scope: subscription()
  properties: {
    principalId: prowPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', contributorRole)
  }
}

resource userAccessAdminRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, prowPrincipalId, userAccessAdminRole)
  scope: subscription()
  properties: {
    principalId: prowPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', userAccessAdminRole)
    condition: restrictedRoleAssignmentCondition
    conditionVersion: restrictedRoleAssignmentConditionVersion
  }
}
