param location string

param keyVaultName string

param subnetId string = ''

param vnetId string = ''

param keyVaultId string

//
//   P R I V A T E   E N D P O I N T
//

var privateDnsZoneName = 'privatelink.vaultcore.azure.net'

resource keyVaultPrivateEndpoint 'Microsoft.Network/privateEndpoints@2024-01-01' = {
  name: '${keyVaultName}-pe'
  location: location
  properties: {
    privateLinkServiceConnections: [
      {
        name: '${keyVaultName}-pe'
        properties: {
          groupIds: [
            'vault'
          ]
          privateLinkServiceId: keyVaultId
        }
      }
    ]
    subnet: {
      id: subnetId
    }
  }
}

resource keyVaultPrivateEndpointDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' = {
  name: privateDnsZoneName
  location: 'global'
  properties: {}
}

resource keyVaultPrivateDnsZoneVnetLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = {
  parent: keyVaultPrivateEndpointDnsZone
  name: uniqueString(keyVaultId)
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: vnetId
    }
  }
}

resource privateEndpointDnsGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2023-09-01' = {
  parent: keyVaultPrivateEndpoint
  name: '${keyVaultName}-dns-group'
  properties: {
    privateDnsZoneConfigs: [
      {
        name: 'config1'
        properties: {
          privateDnsZoneId: keyVaultPrivateEndpointDnsZone.id
        }
      }
    ]
  }
  dependsOn: [
    keyVaultPrivateDnsZoneVnetLink
  ]
}
