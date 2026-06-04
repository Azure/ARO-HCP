/*
Assigns the Cognitive Services OpenAI User role to a principal on an
existing Azure OpenAI resource.

Execution scope: the resource group containing the AOAI resource (regional RG).
Called from mgmt-cluster.bicep with scope: resourceGroup(regionalResourceGroup).
*/

@description('The name of the Azure OpenAI account')
param aoaiName string

@description('Principal ID to assign the role to')
param principalId string

var cognitiveServicesOpenAIUserRoleId = '5e0bd9bd-7b93-4f28-af87-19fc36ad61bd'

resource aoaiAccount 'Microsoft.CognitiveServices/accounts@2024-10-01' existing = {
  name: aoaiName
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aoaiAccount.id, principalId, cognitiveServicesOpenAIUserRoleId)
  scope: aoaiAccount
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', cognitiveServicesOpenAIUserRoleId)
    principalId: principalId
    principalType: 'ServicePrincipal'
  }
}
