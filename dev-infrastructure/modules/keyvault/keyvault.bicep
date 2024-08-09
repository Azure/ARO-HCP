param location string

param keyVaultName string

param subnetId string = ''

param vnetId string = ''

param enableSoftDelete bool

param private bool

// Event for some private KVs it makes sense to disable the creation of a private endpoint,
// e.g. AKS KMS on a private KV will manage their own private endpoint setup in the nodepool RG
param managedPrivateEndpoint bool = true

resource keyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' = {
  location: location
  name: keyVaultName
  tags: {
    resourceGroup: resourceGroup().name
  }
  properties: {
    enableRbacAuthorization: true
    enabledForDeployment: false
    enabledForDiskEncryption: false
    enabledForTemplateDeployment: false
    enableSoftDelete: enableSoftDelete
    publicNetworkAccess: private ? 'Disabled' : 'Enabled'
    sku: {
      name: 'standard'
      family: 'A'
    }
    tenantId: subscription().tenantId
  }
}

//
//   P R I V A T E   E N D P O I N T
//

var privateDnsZoneName = 'privatelink.vaultcore.azure.net'

resource keyVaultPrivateEndpoint 'Microsoft.Network/privateEndpoints@2024-01-01' = if (managedPrivateEndpoint) {
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
          privateLinkServiceId: keyVault.id
        }
      }
    ]
    subnet: {
      id: subnetId
    }
  }
}

resource keyVaultPrivateEndpointDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' = if (managedPrivateEndpoint) {
  name: privateDnsZoneName
  location: 'global'
  properties: {}
}

resource keyVaultPrivateDnsZoneVnetLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = if (managedPrivateEndpoint) {
  parent: keyVaultPrivateEndpointDnsZone
  name: uniqueString(keyVault.id)
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: vnetId
    }
  }
}

resource privateEndpointDnsGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2023-09-01' = if (managedPrivateEndpoint) {
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

output kvName string = keyVault.name
