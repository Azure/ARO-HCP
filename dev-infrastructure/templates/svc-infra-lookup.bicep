@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resource group for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('The subscription ID where the service keyvault resource group lives. Defaults to the current subscription. Set when the keyvault is shared across subscriptions.')
param serviceKeyVaultSubscription string = subscription().subscriptionId

// Nested deployment name is keyed on serviceKeyVaultName so multiple parents
// looking up the same KV in a shared resource group collapse to one slot
// (identical templates → safe Read), and do not collide with svc-infra.bicep's
// create-side nested deployment which is keyed on the resource group id.
module serviceKeyVault '../modules/keyvault/lookup.bicep' = {
  name: 'svc-kv-${uniqueString(serviceKeyVaultName)}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
  }
}

output svcKeyVaultName string = serviceKeyVault.outputs.keyVaultName
output svcKeyVaultUrl string = serviceKeyVault.outputs.keyVaultUrl
