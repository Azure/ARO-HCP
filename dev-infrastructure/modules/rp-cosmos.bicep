// Constants
// Maximum DB account name length is 44
param name string
param disableLocalAuth bool = true

// Passed Params and Overrides
param location string
param zoneRedundant bool
param userAssignedMIs array
param readOnlyUserAssignedMIs array
param private bool

param resourceContainerMaxScale int
param billingContainerMaxScale int
param locksContainerMaxScale int

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
]

param roleDefinitionId string = '00000000-0000-0000-0000-000000000002'
param readOnlyRoleDefinitionId string = '00000000-0000-0000-0000-000000000001'

// Main
resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' = {
  kind: 'GlobalDocumentDB'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: toObject(userAssignedMIs, uami => uami.uamiID, val => {})
  }
  name: name
  location: location
  properties: {
    backupPolicy: {
      type: 'Continuous'
      continuousModeProperties: {
        tier: 'Continuous7Days'
      }
    }
    consistencyPolicy: {
      defaultConsistencyLevel: 'Session'
      maxIntervalInSeconds: 5
      maxStalenessPrefix: 100
    }
    databaseAccountOfferType: 'Standard'
    disableLocalAuth: disableLocalAuth
    locations: [
      {
        locationName: location
        isZoneRedundant: zoneRedundant
      }
    ]
    publicNetworkAccess: private ? 'Disabled' : 'Enabled'
    enableAutomaticFailover: false
    enableMultipleWriteLocations: false
    isVirtualNetworkFilterEnabled: false
    virtualNetworkRules: []
    disableKeyBasedMetadataWriteAccess: false
    enableFreeTier: false
    enableAnalyticalStorage: false
    analyticalStorageConfiguration: {
      schemaType: 'WellDefined'
    }
    createMode: 'Default'
    defaultIdentity: 'FirstPartyIdentity'
    networkAclBypass: 'None'
    enablePartitionMerge: false
    enableBurstCapacity: false
    minimalTlsVersion: 'Tls12'
  }
}

resource cosmosDb 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2023-11-15' = {
  name: name
  parent: cosmosDbAccount
  properties: {
    resource: {
      id: name
    }
    options: {}
  }
}

resource cosmosDbContainers 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2023-11-15' = [
  for c in containers: {
    parent: cosmosDb
    name: c.name
    properties: {
      // Disable till we have way to fix the issue: Updating offer to autoscale throughput is not allowed. Please invoke migration API to migrate this offer.
      //  options: {
      //   autoscaleSettings: {
      //     maxThroughput: c.maxThroughput
      //   }
      // }
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
    name: guid(roleDefinitionId, uami.uamiPrincipalID, cosmosDbAccount.id)
    parent: cosmosDbAccount
    properties: {
      roleDefinitionId: '/${subscription().id}/resourceGroups/${resourceGroup().name}/providers/Microsoft.DocumentDB/databaseAccounts/${cosmosDbAccount.name}/sqlRoleDefinitions/${roleDefinitionId}'
      principalId: uami.uamiPrincipalID
      scope: cosmosDbAccount.id
    }
  }
]

resource sqlRoleAssignmentReadOnly 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = [
  for uami in readOnlyUserAssignedMIs: {
    name: guid(readOnlyRoleDefinitionId, uami.uamiPrincipalID, cosmosDbAccount.id)
    parent: cosmosDbAccount
    properties: {
      roleDefinitionId: '/${subscription().id}/resourceGroups/${resourceGroup().name}/providers/Microsoft.DocumentDB/databaseAccounts/${cosmosDbAccount.name}/sqlRoleDefinitions/${readOnlyRoleDefinitionId}'
      principalId: uami.uamiPrincipalID
      scope: cosmosDbAccount.id
    }
  }
]

output cosmosDBName string = name
output cosmosDBAccountId string = cosmosDbAccount.id
