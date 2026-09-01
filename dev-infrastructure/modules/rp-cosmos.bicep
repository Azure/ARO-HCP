param cosmosDBAccountName string
param userAssignedMIs array
param readOnlyUserAssignedMIs array = []

// Principal ID of the Clusters Service (CS) managed identity. CS is granted
// container-scoped read access to the Fleet container so it can read
// ManagementCluster documents (used to resolve per-management-cluster kube-applier
// containers).
param csManagedIdentityPrincipalId string

param resourceContainerMaxScale int
param billingContainerMaxScale int
param locksContainerMaxScale int
param fleetContainerMaxScale int

var containers = [
  {
    name: 'Resources'
    defaultTtl: -1 // On, no default expiration
    partitionKeyPaths: ['/partitionKey']
    maxThroughput: resourceContainerMaxScale
  }
  {
    name: 'Billing'
    defaultTtl: -1 // On, no default expiration
    partitionKeyPaths: ['/subscriptionId']
    maxThroughput: billingContainerMaxScale
  }
  {
    name: 'Locks'
    defaultTtl: 10
    partitionKeyPaths: ['/id']
    maxThroughput: locksContainerMaxScale
  }
  {
    name: 'Fleet'
    defaultTtl: -1 // On, no default expiration
    partitionKeyPaths: ['/partitionKey']
    maxThroughput: fleetContainerMaxScale
  }
]

// https://learn.microsoft.com/en-us/azure/cosmos-db/reference-data-plane-security#cosmos-db-built-in-data-contributor
param cosmosDataContributorRoleDefinitionId string = '00000000-0000-0000-0000-000000000002'
param cosmosReadOnlyRoleDefinitionId string = '00000000-0000-0000-0000-000000000001'

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  name: cosmosDBAccountName
}

resource cosmosDb 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2023-11-15' existing = {
  name: cosmosDBAccountName
  parent: cosmosDbAccount
}

resource cosmosDbContainers 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2023-11-15' = [
  for c in containers: {
    parent: cosmosDb
    name: c.name
    properties: {
      options: {
        autoscaleSettings: {
          maxThroughput: c.maxThroughput
        }
      }
      resource: {
        id: c.name
        defaultTtl: c.defaultTtl
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
          paths: c.partitionKeyPaths
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
]

resource sqlRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = [
  for uami in userAssignedMIs: {
    name: guid(cosmosDataContributorRoleDefinitionId, uami.uamiPrincipalID, cosmosDbAccount.id)
    parent: cosmosDbAccount
    properties: {
      roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${cosmosDataContributorRoleDefinitionId}'
      principalId: uami.uamiPrincipalID
      scope: cosmosDbAccount.id
    }
  }
]

resource sqlRoleAssignmentReadOnly 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = [
  for uami in readOnlyUserAssignedMIs: {
    name: guid(cosmosReadOnlyRoleDefinitionId, uami.uamiPrincipalID, cosmosDbAccount.id)
    parent: cosmosDbAccount
    properties: {
      roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${cosmosReadOnlyRoleDefinitionId}'
      principalId: uami.uamiPrincipalID
      scope: cosmosDbAccount.id
    }
  }
]

// Container-scoped read access to the Fleet container for the Clusters Service
// managed identity. CS reads ManagementCluster documents from Fleet to resolve the
// per-management-cluster kube-applier container it must write to.
var fleetContainerScope = '${cosmosDbAccount.id}/dbs/${cosmosDBAccountName}/colls/Fleet'

resource sqlRoleAssignmentFleetReadOnly 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = {
  name: guid(cosmosReadOnlyRoleDefinitionId, csManagedIdentityPrincipalId, fleetContainerScope)
  parent: cosmosDbAccount
  properties: {
    roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${cosmosReadOnlyRoleDefinitionId}'
    principalId: csManagedIdentityPrincipalId
    scope: fleetContainerScope
  }
}

// Container-scoped read/write access to the Resources container for the Clusters
// Service managed identity, so CS can persist its controller documents (per team
// decision).
var resourcesContainerScope = '${cosmosDbAccount.id}/dbs/${cosmosDBAccountName}/colls/Resources'

resource sqlRoleAssignmentResourcesReadWrite 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = {
  name: guid(cosmosDataContributorRoleDefinitionId, csManagedIdentityPrincipalId, resourcesContainerScope)
  parent: cosmosDbAccount
  properties: {
    roleDefinitionId: '${cosmosDbAccount.id}/sqlRoleDefinitions/${cosmosDataContributorRoleDefinitionId}'
    principalId: csManagedIdentityPrincipalId
    scope: resourcesContainerScope
  }
}
