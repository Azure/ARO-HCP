@description('Name of the existing KeyVault containing the encryption key')
param keyVaultName string

@description('Name of the existing encryption key in the KeyVault')
param etcdEncryptionKeyName string = 'etcd-data-kms-encryption-key'

@description('Cluster name used to ensure unique resource names within the resource group')
param clusterName string = ''

@description('Principal ID of the service managed identity to grant Reader on the DES. If empty, no role assignment is created.')
param serviceMiPrincipalId string = ''

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

// Reader: allows the service managed identity to read the DES for validation
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

resource serviceMiReaderRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (serviceMiPrincipalId != '') {
  name: guid(resourceGroup().id, diskEncryptionSet.id, readerRoleId, serviceMiPrincipalId)
  scope: diskEncryptionSet
  properties: {
    principalId: serviceMiPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

@description('The resource ID of the created DiskEncryptionSet')
output diskEncryptionSetId string = diskEncryptionSet.id
