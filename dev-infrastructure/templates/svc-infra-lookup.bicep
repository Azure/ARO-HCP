@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resource group for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('The subscription ID where the service keyvault resource group lives. Defaults to the current subscription. Set when the keyvault is shared across subscriptions.')
param serviceKeyVaultSubscription string = subscription().subscriptionId

// this is mostly a situation where multiple svc-infra pipelines run towards
// a shared svc keyvault resource group ${serviceKeyVaultResourceGroup}. while
// the individual modules will not conflict with each other, the existence
// of same-named deployments fails one pipeline.
var deploymentNameSuffix = uniqueString(resourceGroup().id)

module serviceKeyVault '../modules/keyvault/lookup.bicep' = {
  name: 'svc-kv-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
  }
}

output svcKeyVaultName string = serviceKeyVault.outputs.keyVaultName
output svcKeyVaultUrl string = serviceKeyVault.outputs.keyVaultUrl
