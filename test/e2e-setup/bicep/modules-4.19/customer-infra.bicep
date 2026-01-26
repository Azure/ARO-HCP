@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool = false

@description('Network Security Group Name')
param customerNsgName string = 'customer-nsg'

@description('Virtual Network Name')
param customerVnetName string = 'customer-vnet'

@description('Subnet Name')
param customerVnetSubnetName string = 'customer-subnet-1'

@description('The name of the encryption key for etcd')
param customerEtcdEncryptionKeyName string = 'etcd-data-kms-encryption-key'

//
// Variables
//

var randomSuffix = toLower(uniqueString(resourceGroup().id))

// The Key Vault Name is defined here in a variable instead of using a
// parameter because of strict Azure requirements for KeyVault names
// (KeyVault names are globally unique and must be between 3-24 alphanumeric
// characters).
var customerKeyVaultName string = 'cust-kv-${randomSuffix}'

//
// Network
//

var addressPrefix = '10.0.0.0/16'
var subnetPrefix = '10.0.0.0/24'

resource customerNsg 'Microsoft.Network/networkSecurityGroups@2023-05-01' = {
  name: customerNsgName
  location: resourceGroup().location
  tags: {
    persist: string(persistTagValue)
  }
}

resource customerVnet 'Microsoft.Network/virtualNetworks@2023-05-01' = {
  name: customerVnetName
  location: resourceGroup().location
  tags: {
    persist: string(persistTagValue)
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

//
// KeyVault
//

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
  name: customerEtcdEncryptionKeyName
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

@description('The name of the encryption key for etcd')
output etcdEncryptionKeyName string = customerEtcdEncryptionKeyName

@description('Network Security Group Resource ID')
output nsgID string = customerNsg.id

@description('Customer VNet Subnet Resource ID')
output vnetSubnetID string = '${customerVnet.id}/subnets/${customerVnetSubnetName}'

@description('The version of the etcd encryption key')
output etcdEncryptionKeyVersion string = last(split(etcdEncryptionKey.properties.keyUriWithVersion, '/'))
