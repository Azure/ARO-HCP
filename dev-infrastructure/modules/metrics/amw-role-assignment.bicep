@description('The name of the existing Azure Monitor Workspace in the deployment resource group')
param workspaceName string

@description('The managed identity principal ID, resolved in its original subscription')
param principalId string

@description('The role definition ID (GUID)')
param roleDefinitionId string

@description('Existing assignment name to preserve when referencing a previously owned workspace')
param roleAssignmentName string = ''

resource workspace 'Microsoft.Monitor/accounts@2021-06-03-preview' existing = {
  name: workspaceName
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: empty(roleAssignmentName) ? guid(toLower(workspace.id), principalId, roleDefinitionId) : roleAssignmentName
  scope: workspace
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', roleDefinitionId)
  }
}

output roleAssignmentId string = roleAssignment.id
