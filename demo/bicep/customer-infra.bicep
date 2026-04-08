@description('Network Security Group Name')
param customerNsgName string

@description('Virtual Network Name')
param customerVnetName string

@description('Subnet Name')
param customerVnetSubnetName string

@description('Virtual Network Integration Subnet Name')
param customerVirtualNetworkIntegrationSubnetName string

@description('If the key vault used for etcd encryption should be public or private')
param privateKeyVault bool = true

var randomSuffix = toLower(uniqueString(resourceGroup().id))

// The Key Vault Name is defined here in a variable instead of using a
// parameter because of strict Azure requirements for KeyVault names
// (KeyVault names are globally unique and must be between 3-24 alphanumeric
// characters).
var customerKeyVaultName string = 'cust-kv-${randomSuffix}'

var addressPrefix = '10.0.0.0/16'
var subnetPrefix = '10.0.0.0/24'
var virtualNetworkIntegrationSubnetPrefix = '10.0.1.0/24'

resource customerNsg 'Microsoft.Network/networkSecurityGroups@2023-05-01' = {
  name: customerNsgName
  location: resourceGroup().location
  tags: {
    persist: 'true'
  }
}

resource customerVnet 'Microsoft.Network/virtualNetworks@2023-05-01' = {
  name: customerVnetName
  location: resourceGroup().location
  tags: {
    persist: 'true'
  }
  properties: {
    addressSpace: {
      addressPrefixes: [
        addressPrefix
      ]
    }
    subnets: [
      {
        name: customerVnetSubnetName
        properties: {
          addressPrefix: subnetPrefix
          networkSecurityGroup: {
            id: customerNsg.id
          }
        }
      }
      {
        name: customerVirtualNetworkIntegrationSubnetName
        properties: {
          addressPrefix: virtualNetworkIntegrationSubnetPrefix
          delegations: [
            {
              name: 'aro-hcp-delegation'
              properties: {
                serviceName: 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters'
              }
            }
          ]
        }
      }
    ]
  }
}

resource customerKeyVault 'Microsoft.KeyVault/vaults@2024-12-01-preview' = {
  name: customerKeyVaultName
  location: resourceGroup().location
  properties: {
    enableRbacAuthorization: true
    enableSoftDelete: false
    tenantId: subscription().tenantId
    publicNetworkAccess: privateKeyVault ? 'Disabled' : 'Enabled'
    sku: {
      family: 'A'
      name: 'standard'
    }
  }
}

resource etcdEncryptionKey 'Microsoft.KeyVault/vaults/keys@2024-12-01-preview' = {
  parent: customerKeyVault
  name: 'etcd-data-kms-encryption-key'
  properties: {
    kty: 'RSA'
    keySize: 2048
  }
}

resource privateEndpointDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' = if (privateKeyVault) {
  name: 'privatelink.vaultcore.azure.net'
  location: 'global'
  properties: {}
  dependsOn: [
    privateEndpoint
  ]
}

resource privateEndpoint 'Microsoft.Network/privateEndpoints@2023-09-01' = if (privateKeyVault) {
  name: 'kv-private-endpoint'
  properties: {
    privateLinkServiceConnections: [
      {
        name: 'kv-private-endpoint'
        properties: {
          privateLinkServiceId: customerKeyVault.id
          groupIds: ['vault']
        }
      }
    ]
    subnet: {
      id: customerVnet.properties.subnets[0].id
    }
  }
  location: resourceGroup().location
}

resource privateEndpointDnsGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2023-09-01' = if (privateKeyVault) {
  name: 'kv-private-ep-dns-group'
  parent: privateEndpoint
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

resource privateDnsZoneVnetLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = if (privateKeyVault) {
  name: uniqueString('kv-private-dns-zone-link')
  parent: privateEndpointDnsZone
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: customerVnet.id
    }
  }
}

output subnetId string = '${customerVnet.id}/subnets/${customerVnetSubnetName}'
output vnetIntegrationSubnetId string = '${customerVnet.id}/subnets/${customerVirtualNetworkIntegrationSubnetName}'
output networkSecurityGroupId string = customerNsg.id
output keyVaultName string = customerKeyVaultName
output etcdEncryptionKeyVersion string = last(split(etcdEncryptionKey.properties.keyUriWithVersion, '/'))
