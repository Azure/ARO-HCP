param name string = '${resourceGroup().name}-rp-cosmos'
param disableLocalAuth bool = true
param location string
param aksNodeSubnetId string
param vnetId string

var containerNames = [
  'Subscriptions'
  'AsyncOperations'
  'Clusters'
  'Billing'
]

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' = {
  kind: 'GlobalDocumentDB'
  identity: {
    type: 'None'
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
      }
    ]
    publicNetworkAccess: 'Disabled'
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

resource cosmosDbPrivateEndpoint 'Microsoft.Network/privateEndpoints@2023-09-01' = {
  name: '${name}-private-endpoint'
  location: location
  properties: {
    privateLinkServiceConnections: [
      {
        name: '${name}-private-endpoint'
        properties: {
          privateLinkServiceId: cosmosDbAccount.id
          groupIds: [
            'Sql'
          ]
        }
      }
    ]
    subnet: {
      id: aksNodeSubnetId
    }
  }
}

resource cosmosPrivateEndpointDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' = {
  // https://github.com/Azure/bicep/issues/12482
  // There is no environments().suffixes constant for this
  name: 'privatelink.documents.azure.com'
  location: 'global'
  properties: {}
}

resource cosmosPrivateEndpointDnsZoneLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = {
  parent: cosmosPrivateEndpointDnsZone
  name: 'link'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: vnetId
    }
  }
}

resource cosmosPrivateEndpointDnsGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2023-09-01' = {
  parent: cosmosDbPrivateEndpoint
  name: '${name}-dns-group'
  properties: {
    privateDnsZoneConfigs: [
      {
        name: 'config1'
        properties: {
          privateDnsZoneId: cosmosPrivateEndpointDnsZone.id
        }
      }
    ]
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
  for containerName in containerNames: {
    parent: cosmosDb
    name: containerName
    properties: {
      resource: {
        id: containerName
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
          paths: [
            '/Id'
          ]
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
