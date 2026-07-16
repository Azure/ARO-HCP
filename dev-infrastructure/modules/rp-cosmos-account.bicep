// Maximum DB account name length is 44
param name string
param disableLocalAuth bool = true
param location string
param zoneRedundant bool
param private bool

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2024-11-15' = {
  kind: 'GlobalDocumentDB'
  name: name
  location: location
  properties: {
    // UPGRADE-PATH REGRESSION TEST (not for merge): the Cosmos DB backup
    // policy type is immutable once an account exists. Azure allows a
    // one-way Periodic -> Continuous migration but rejects switching a
    // Continuous account back to Periodic with a BadRequest. A fresh RP
    // deploy creates the account directly as Periodic and succeeds, so
    // e2e-parallel stays green; an upgrade over a baseline provisioned
    // from main (which uses Continuous) hits the disallowed
    // Continuous -> Periodic transition and fails the
    // aro-hcp-upgrade-environment step. This validates that the
    // upgrade-e2e-parallel job catches upgrade-only infra regressions.
    backupPolicy: {
      type: 'Periodic'
      periodicModeProperties: {
        backupIntervalInMinutes: 240
        backupRetentionIntervalInHours: 8
        backupStorageRedundancy: 'Local'
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
    enablePerRegionPerPartitionAutoscale: true
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
          'Microsoft.DocumentDB/databaseAccounts/readMetadata'
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
