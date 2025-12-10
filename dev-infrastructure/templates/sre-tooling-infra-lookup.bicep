@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resource group for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

// this is mostly a situation where multiple sre-tooling-infra pipelines run towards
// a shared service keyvault resource group ${serviceKeyVaultResourceGroup}. while
// the individual modules will not conflict with each other, the existence
// of same-named deployments fails one pipeline.
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
