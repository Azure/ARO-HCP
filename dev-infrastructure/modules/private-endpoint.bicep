param location string

param serviceType string
param subnetIds array

param privateLinkServiceId string

param groupIds array

param privateEndpointDnsZoneName string

param vnetId string

resource privateEndpointDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' existing = {
  name: privateEndpointDnsZoneName
}

resource eventGridPrivatEndpoint 'Microsoft.Network/privateEndpoints@2023-09-01' = [
  for aksNodeSubnetId in subnetIds: {
    name: '${serviceType}-${uniqueString(aksNodeSubnetId)}'
    location: location
    properties: {
      privateLinkServiceConnections: [
        {
          name: '${serviceType}-private-endpoint'
          properties: {
            privateLinkServiceId: privateLinkServiceId
            groupIds: groupIds
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
    name: '${serviceType}-dns-group'
    parent: eventGridPrivatEndpoint[index]
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
  }
]

resource eventGridPrivateDnsZoneVnetLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = {
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
