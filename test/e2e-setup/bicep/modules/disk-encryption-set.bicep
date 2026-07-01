@description('Name of the existing KeyVault containing the encryption key')
param keyVaultName string

@description('Name of the existing encryption key in the KeyVault')
param etcdEncryptionKeyName string = 'etcd-data-kms-encryption-key'

@description('Cluster name used to ensure unique resource names within the resource group')
param clusterName string = ''

var randomSuffix = toLower(uniqueString(resourceGroup().id, clusterName))

resource keyVault 'Microsoft.KeyVault/vaults@2024-12-01-preview' existing = {
  name: keyVaultName
}

resource etcdEncryptionKey 'Microsoft.KeyVault/vaults/keys@2024-12-01-preview' existing = {
  parent: keyVault
  name: etcdEncryptionKeyName
}

resource diskEncryptionSet 'Microsoft.Compute/diskEncryptionSets@2023-10-02' = {
  name: '${clusterName}-des-${randomSuffix}'
  location: resourceGroup().location
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    activeKey: {
      sourceVault: {
        id: keyVault.id
      }
      keyUrl: etcdEncryptionKey.properties.keyUriWithVersion
    }
    encryptionType: 'EncryptionAtRestWithCustomerKey'
  }
}

// Key Vault Crypto Service Encryption User: allows the DES to wrap/unwrap keys
// https://www.azadvertizer.net/azrolesadvertizer/e147488a-f6f5-4113-8e2d-b22465e65bf6.html
var kvCryptoServiceEncryptionUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'e147488a-f6f5-4113-8e2d-b22465e65bf6'
)

resource desKeyVaultRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, diskEncryptionSet.id, kvCryptoServiceEncryptionUserRoleId, keyVault.id)
  scope: keyVault
  properties: {
    principalId: diskEncryptionSet.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: kvCryptoServiceEncryptionUserRoleId
  }
}

@description('The resource ID of the created DiskEncryptionSet')
output diskEncryptionSetId string = diskEncryptionSet.id
