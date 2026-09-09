targetScope = 'subscription'

@description('The principal ID of the Exporter managed identity')
param exporterPrincipalId string

@description('The service Key Vault name')
param serviceKeyVaultName string

@description('The resource group containing the service Key Vault')
param serviceKeyVaultResourceGroup string

@description('The subscription ID containing the service Key Vault')
param serviceKeyVaultSubscription string

var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// Azure Resource Graph only returns clusters the exporter can read, so discovery requires subscription scope.
resource aroHcpExporterReaderSvc 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, exporterPrincipalId, readerRoleId)
  scope: subscription()
  properties: {
    principalId: exporterPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

module aroHcpExporterKeyVaultReader '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'exporter-kv-reader-${uniqueString(exporterPrincipalId)}'
  scope: resourceGroup(serviceKeyVaultSubscription, serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Reader'
    managedIdentityPrincipalIds: [exporterPrincipalId]
  }
}
