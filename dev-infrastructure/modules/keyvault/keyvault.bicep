@description('Location of the keyvault.')
param location string

@description('Name of the key vault.')
param keyVaultName string

@description('Toggle to enable soft delete.')
param enableSoftDelete bool

@description('Toggle to make the keyvault private.')
param private bool

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

output kvId string = keyVault.id

output kvName string = keyVault.name
