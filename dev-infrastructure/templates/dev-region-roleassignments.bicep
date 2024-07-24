// This is used to grant CI the ability to act on the SVC keyvault
// and potentially other resources in the SC RG (a.k.a. regional RG)
param serviceKeyVaultName string
param githubActionsPrincipalID string

module csServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(serviceKeyVaultName, 'ghci', 'read')
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: githubActionsPrincipalID
  }
}
