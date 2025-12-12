@description('Key Vault name')
param keyVaultName string

@description('Key Vault resource group')
param keyVaultResourceGroup string = resourceGroup().name

module keyVault '../../../modules/keyvault/lookup.bicep' = {
  name: 'sre-tooling-kv-lookup'
  scope: resourceGroup(keyVaultResourceGroup)
  params: {
    keyVaultName: keyVaultName
  }
}

output sreToolingKeyVaultName string = keyVault.outputs.keyVaultName
output sreToolingKeyVaultUrl string = keyVault.outputs.keyVaultUrl

