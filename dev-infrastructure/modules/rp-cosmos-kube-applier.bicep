param cosmosDBAccountName string
param containerName string
param containerMaxScale int
param kubeApplierManagedIdentityPrincipalId string

// https://learn.microsoft.com/en-us/azure/cosmos-db/reference-data-plane-security#cosmos-db-built-in-data-contributor
param cosmosDataContributorRoleDefinitionId string = '00000000-0000-0000-0000-000000000002'

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  name: cosmosDBAccountName
}

resource cosmosDb 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2023-11-15' existing = {
  name: cosmosDBAccountName
  parent: cosmosDbAccount
}

resource container 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2023-11-15' = {
  parent: cosmosDb
  name: containerName
  properties: {
    options: {
      autoscaleSettings: {
        maxThroughput: containerMaxScale
      }
    }
    resource: {
      id: containerName
      defaultTtl: -1
      indexingPolicy: {
        indexingMode: 'consistent'
        automatic: true
        includedPaths: [
          {
            path: '/*'
          }
        ]
        excludedPaths: [
          {
            path: '/"_etag"/?'
          }
        ]
      }
      partitionKey: {
        paths: ['/partitionKey']
        kind: 'Hash'
        version: 2
      }
      uniqueKeyPolicy: {
        uniqueKeys: []
      }
      conflictResolutionPolicy: {
        mode: 'LastWriterWins'
        conflictResolutionPath: '/_ts'
      }
      computedProperties: []
    }
  }
}

var containerScope = '${cosmosDbAccount.id}/dbs/${cosmosDBAccountName}/colls/${containerName}'

resource sqlRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = {
  name: guid(
    guid('kube-applier-role', cosmosDbAccount.id, cosmosDataContributorRoleDefinitionId),
    kubeApplierManagedIdentityPrincipalId,
    container.id
  )
  parent: cosmosDbAccount
  properties: {
    roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${cosmosDataContributorRoleDefinitionId}'
    principalId: kubeApplierManagedIdentityPrincipalId
    scope: containerScope
  }
}
