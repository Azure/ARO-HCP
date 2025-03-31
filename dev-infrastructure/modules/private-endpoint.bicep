param location string

@description('The service type the private endpoint is created for')
@allowed([
  'eventgrid'
  'keyvault'
  'cosmosdb'
  'postgres'
  'acr'
])
param serviceType string

@description('The group id of the private endpoint service')
@allowed([
  'topicspace'
  'vault'
  'Sql'
  'postgresqlServer'
  'registry'
])
param groupId string

@description('The private link service id')
param privateLinkServiceId string

@description('The subnet ids to create the private endpoint in')
param subnetIds array

@description('The vnet id to link the private endpoint to')
param vnetId string

var endpointConfig = {
  eventgrid: {
    topicspace: 'privatelink.ts.eventgrid.azure.net'
  }
  keyvault: {
    vault: 'privatelink.vaultcore.azure.net'
  }
  cosmosdb: {
    Sql: 'privatelink.documents.azure.com'
  }
  postgres: {
    postgresqlServer: 'privatelink.postgres.database.azure.com'
  }
  acr: {
    registry: 'privatelink.azurecr.io'
  }
}

resource privateEndpointDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' = {
  name: endpointConfig[serviceType][groupId]
  location: 'global'
  properties: {}
  dependsOn: [
    privatEndpoint
  ]
}

resource privatEndpoint 'Microsoft.Network/privateEndpoints@2023-09-01' = [
  for aksNodeSubnetId in subnetIds: {
    name: '${serviceType}-${uniqueString(aksNodeSubnetId, privateLinkServiceId)}'
    location: location
    properties: {
      privateLinkServiceConnections: [
        {
          name: '${serviceType}-private-endpoint'
          properties: {
            privateLinkServiceId: privateLinkServiceId
            groupIds: [groupId]
          }
        }
      ]
      subnet: {
        id: aksNodeSubnetId
      }
    }
  }
]

resource privateEndpointDnsGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2023-09-01' = [
  for index in range(0, length(subnetIds)): {
    name: '${serviceType}-${uniqueString(subnetIds[index])}'
    parent: privatEndpoint[index]
    properties: {
      privateDnsZoneConfigs: [
        {
          name: 'config1'
          properties: {
            privateDnsZoneId: privateEndpointDnsZone.id
          }
        }
      ]
    }
    dependsOn: [
      privateDnsZoneVnetLink
    ]
  }
]

resource privateDnsZoneVnetLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = {
  name: uniqueString('eventgrid-${uniqueString(vnetId)}')
  parent: privateEndpointDnsZone
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: vnetId
    }
  }
}
