@description('The name of the keyvault where access is managed')
param keyVaultName string

@description('Optional name of the secret in the keyvault where access is managed. If not provided, access will be granted to all secrets in the keyvault.')
param secretName string = ''

@description('The name of the role that will be assigned to the managed identity for the secret in KV')
@allowed([
  'Key Vault Secrets Officer'
  'Key Vault Secrets User'
])
param roleName string

@description('The principal id of the managed identity that will be assigned access to the secret in KV')
param managedIdentityPrincipalId string

var roleResourceIds = {
  // Perform any action on the secrets of a key vault, except manage permissions.
  'Key Vault Secrets User': subscriptionResourceId(
    'Microsoft.Authorization/roleDefinitions/',
    '4633458b-17de-408a-b874-0445c86b69e6'
  )
  // Read secret contents including secret portion of a certificate with private key.
  'Key Vault Secrets Officer': subscriptionResourceId(
    'Microsoft.Authorization/roleDefinitions/',
    'b86a8fe4-44ce-4948-aee5-eccb2c155cd7'
  )
}

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource secret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' existing = if (secretName != '') {
  parent: kv
  name: secretName
}

resource secretAccessPermission 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (secretName != '') {
  scope: secret
  name: guid(kv.id, managedIdentityPrincipalId, secretName, roleResourceIds[roleName])
  properties: {
    roleDefinitionId: roleResourceIds[roleName]
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource keyVaultAccessPermission 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (secretName == '') {
  scope: kv
  name: guid(kv.id, managedIdentityPrincipalId, roleResourceIds[roleName])
  properties: {
    roleDefinitionId: roleResourceIds[roleName]
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}
