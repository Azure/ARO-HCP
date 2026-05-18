param cosmosDBAccountName string
param containerName string
param containerMaxScale int
param kubeApplierManagedIdentityPrincipalId string

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

var kubeApplierRoleDefinitionId = guid('kube-applier-role', cosmosDbAccount.id)
var containerScope = '${cosmosDbAccount.id}/dbs/${cosmosDBAccountName}/colls/${containerName}'

resource sqlRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = {
  name: guid(kubeApplierRoleDefinitionId, kubeApplierManagedIdentityPrincipalId, container.id)
  parent: cosmosDbAccount
  properties: {
    roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${kubeApplierRoleDefinitionId}'
    principalId: kubeApplierManagedIdentityPrincipalId
    scope: containerScope
  }
}
