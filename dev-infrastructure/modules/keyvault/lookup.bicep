@description('The name of the keyvault')
param keyVaultName string

resource keyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: keyVaultName
}

output keyVaultName string = keyVault.name
output keyVaultUrl string = keyVault.properties.vaultUri
