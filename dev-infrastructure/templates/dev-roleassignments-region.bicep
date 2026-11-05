// This is used to grant CI access to the dev Cosmos DB account.
// It should not be used in higher environments.
//
// This template is deployed to the regional resource group, which is
// where the Cosmos DB account lives. AKS cluster role assignments live
// in the service cluster resource group, see dev-roleassignments-svc.bicep.
param grantCosmosAccess bool = false
param cosmosDBName string = 'replaceme'
param principalID string

// Grants Github Actions access to Cosmos data
param cosmosRoleDefinitionId string = '00000000-0000-0000-0000-000000000002'
var cosmosRoleAssignmentId = guid(cosmosRoleDefinitionId, principalID, cosmosDbAccount.id)

// C O S M O S

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = if (grantCosmosAccess) {
  name: cosmosDBName
}

resource sqlRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = if (grantCosmosAccess) {
  name: cosmosRoleAssignmentId
  parent: cosmosDbAccount
  properties: {
    roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${cosmosRoleDefinitionId}'
    principalId: principalID
    scope: cosmosDbAccount.id
  }
}
