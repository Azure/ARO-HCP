// Maximum DB account name length is 44
param name string
param disableLocalAuth bool = true
param location string
param zoneRedundant bool
param private bool

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' = {
  kind: 'GlobalDocumentDB'
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

// Custom SQL role for kube-applier: read, replace, query, change feed (no create/delete)
var kubeApplierRoleDefinitionId = guid('kube-applier-role', cosmosDbAccount.id)

resource kubeApplierSqlRoleDefinition 'Microsoft.DocumentDB/databaseAccounts/sqlRoleDefinitions@2021-04-15' = {
  parent: cosmosDbAccount
  name: kubeApplierRoleDefinitionId
  properties: {
    roleName: 'Kube Applier Reader Writer'
    type: 'CustomRole'
    assignableScopes: [
      cosmosDbAccount.id
    ]
    permissions: [
      {
        dataActions: [
          'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/items/read'
          'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/items/replace'
          'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/executeQuery'
          'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/readChangeFeed'
        ]
      }
    ]
  }
}

output cosmosDBName string = name
output cosmosDBAccountId string = cosmosDbAccount.id
output kubeApplierSqlRoleDefinitionId string = kubeApplierSqlRoleDefinition.id
