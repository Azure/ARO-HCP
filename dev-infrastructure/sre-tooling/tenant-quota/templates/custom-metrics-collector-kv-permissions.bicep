@description('Managed Identity client ID')
param msiClientId string

@description('Key Vault name')
param keyVaultName string

@description('Key Vault resource group')
param keyVaultResourceGroup string = resourceGroup().name

module kvAccess '../../../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'custom-metrics-collector-kv-access'
  scope: resourceGroup(keyVaultResourceGroup)
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [msiClientId]
    secretName: ''
  }
}

