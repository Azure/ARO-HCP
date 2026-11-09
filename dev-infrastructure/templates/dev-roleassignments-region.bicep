// This is used to grant personal dev environments access to the dev
// Cosmos DB account. It should not be used in higher environments.
//
// This template is deployed to the regional resource group, which is
// where the Cosmos DB account lives. AKS cluster role assignments live
// in the service cluster resource group, see dev-roleassignments-svc.bicep.
param cosmosDBName string
param principalID string

// Grants access to Cosmos data
param cosmosRoleDefinitionId string = '00000000-0000-0000-0000-000000000002'
var cosmosRoleAssignmentId = guid(cosmosRoleDefinitionId, principalID, cosmosDbAccount.id)

// C O S M O S

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  name: cosmosDBName
}

resource sqlRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = {
  name: cosmosRoleAssignmentId
  parent: cosmosDbAccount
  properties: {
    roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${cosmosRoleDefinitionId}'
    principalId: principalID
    scope: cosmosDbAccount.id
  }
}
