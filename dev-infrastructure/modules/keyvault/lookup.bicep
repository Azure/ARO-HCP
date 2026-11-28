@description('The name of the keyvault')
param keyVaultName string

resource keyVault 'Microsoft.KeyVault/vaults@2026-02-01' existing = {
  name: keyVaultName
}

output keyVaultName string = keyVault.name
output keyVaultUrl string = keyVault.properties.vaultUri
