@description('The name of the key vault')
param keyVaultName string

@description('Principal ID of the cluster service')
param csManagedIdentityPrincipalId string

module csServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(keyVaultName, 'cs', 'read')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: csManagedIdentityPrincipalId
  }
}
