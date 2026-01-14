@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resource group for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

var deploymentNameSuffix = uniqueString(resourceGroup().id)

module serviceKeyVault '../modules/keyvault/lookup.bicep' = {
  name: 'sre-tooling-kv-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
  }
}

output sreToolingKeyVaultName string = serviceKeyVault.outputs.keyVaultName
output sreToolingKeyVaultUrl string = serviceKeyVault.outputs.keyVaultUrl
