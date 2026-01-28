@description('Network Security Group Name')
param customerNsgName string

@description('Virtual Network Name')
param customerVnetName string

@description('Subnet Name')
param customerVnetSubnetName string

var randomSuffix = toLower(uniqueString(resourceGroup().id))

// The Key Vault Name is defined here in a variable instead of using a
// parameter because of strict Azure requirements for KeyVault names
// (KeyVault names are globally unique and must be between 3-24 alphanumeric
// characters).
var customerKeyVaultName string = 'cust-kv-${randomSuffix}'

var addressPrefix = '10.0.0.0/16'
var subnetPrefix = '10.0.0.0/23'

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


output subnetId string = customerVnet.properties.subnets[0].id
output networkSecurityGroupId string = customerNsg.id
output keyVaultName string = customerKeyVaultName
