@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool

@description('Network Security Group Name')
param customerNsgName string = 'customer-nsg'

@description('Virtual Network Name')
param customerVnetName string = 'customer-vnet'

@description('Subnet Name')
param customerVnetSubnetName string = 'customer-subnet-1'

@description('Key Vault Name')
param customerKeyVaultName string = 'customer-key-vault'

var addressPrefix = '10.0.0.0/16'
var subnetPrefix = '10.0.0.0/24'

resource customerNsg 'Microsoft.Network/networkSecurityGroups@2023-05-01' = {
  name: customerNsgName
  location: resourceGroup().location
  tags: {
    persist: persistTagValue
  }
}

resource customerVnet 'Microsoft.Network/virtualNetworks@2023-05-01' = {
  name: customerVnetName
  location: resourceGroup().location
  tags: {
    persist: persistTagValue
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

//
// outputs
//

@description('Network Security Group Name')
output nsgName string = customerNsgName

@description('Virtual Network Name')
output vnetName string = customerVnetName

@description('Subnet Name')
output vnetSubnetName string = customerVnetSubnetName

@description('Key Vault Name')
output keyVaultName string = customerKeyVaultName
