/*
Grants a managed identity access to a certificate secret in Key Vault.

Execution scope: the resourcegroup of the Key Vault
*/

@description('The Key Vault name')
param keyVaultName string

@description('The name of the secret (certificate name) to grant access to')
param secretName string

@description('The principal ID of the managed identity to grant access to')
param principalId string

var keyVaultSecretUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4633458b-17de-408a-b874-0445c86b69e6'
)

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource secret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' existing = {
  parent: kv
  name: secretName
}

resource secretAccessPermission 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: secret
  name: guid(principalId, keyVaultSecretUserRoleId, kv.id, secretName)
  properties: {
    roleDefinitionId: keyVaultSecretUserRoleId
    principalId: principalId
    principalType: 'ServicePrincipal'
  }
}
