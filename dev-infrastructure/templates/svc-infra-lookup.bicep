@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resource group for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('The subscription ID where the service keyvault resource group lives. Defaults to the current subscription. Set when the keyvault is shared across subscriptions.')
param serviceKeyVaultSubscription string = subscription().subscriptionId

resource serviceKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: serviceKeyVaultName
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
}

output svcKeyVaultName string = serviceKeyVault.name
output svcKeyVaultUrl string = serviceKeyVault.properties.vaultUri
