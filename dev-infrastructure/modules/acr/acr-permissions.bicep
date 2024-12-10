@description('The principal id of the service principal that will be assigned access to the ACR')
param principalId string

@description('Whether to grant push access to the ACR')
param grantPushAccess bool = false

@description('Whether to grant manage token access to the ACR')
param grantManageTokenAccess bool = false

@description('''
  The custom token management role might not be available in an environment due to quota limitations.
  In such cases, the default ACR Contributor and Data Access Configuration Administrator role will
  be used for token management permissions.
  ''')
param useCustomManageTokenRole bool = false

@description('ACR Namespace Resource Group Id')
param acrResourceGroupid string

// https://www.azadvertizer.net/azrolesadvertizer/7f951dda-4ed3-4680-a7ca-43fe172d538d.html
var acrPullRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

// https://www.azadvertizer.net/azrolesadvertizer/8311e382-0749-4cb8-b61a-304f252e45ec.html
var acrPushRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '8311e382-0749-4cb8-b61a-304f252e45ec'
)

// https://www.azadvertizer.net/azrolesadvertizer/c2f4ef07-c644-48eb-af81-4b1b4947fb11.html
var acrDeleteRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'c2f4ef07-c644-48eb-af81-4b1b4947fb11'
)

// https://www.azadvertizer.net/azrolesadvertizer/3bc748fc-213d-45c1-8d91-9da5725539b9.html
var acrContributorAndDataAccessConfigurationAdministratorRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '3bc748fc-213d-45c1-8d91-9da5725539b9'
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

//
// Custom role for token management permissions
//

import * as tmr from 'token-role-name.bicep'

resource tokenManagementRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' existing = if (grantManageTokenAccess && useCustomManageTokenRole) {
  name: guid(tmr.tokenManagementRoleName)
}

resource acrContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (grantManageTokenAccess && useCustomManageTokenRole) {
  name: guid(acrResourceGroupid, principalId, 'token-creation-role')
  properties: {
    roleDefinitionId: tokenManagementRole.id
    principalId: principalId
    principalType: 'ServicePrincipal'
  }
}

//
// Built-in wider role for token management permissions
//

resource acrContributorAndDataAccessConfigurationAdministratorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (grantManageTokenAccess && !useCustomManageTokenRole) {
  name: guid(acrResourceGroupid, principalId, acrContributorAndDataAccessConfigurationAdministratorRoleDefinitionId)
  properties: {
    roleDefinitionId: acrContributorAndDataAccessConfigurationAdministratorRoleDefinitionId
    principalId: principalId
    principalType: 'ServicePrincipal'
  }
}
