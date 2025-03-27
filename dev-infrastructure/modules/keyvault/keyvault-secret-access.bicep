@description('The name of the keyvault where access is managed')
param keyVaultName string

@description('Optional name of the secret in the keyvault where access is managed. If not provided, access will be granted to all secrets in the keyvault.')
param secretName string = ''

@description('The name of the role that will be assigned to the managed identity for the secret in KV')
@allowed([
  'Key Vault Secrets Officer'
  'Key Vault Secrets User'
  'Key Vault Certificate User'
  'Key Vault Certificates Officer'
  'Key Vault Crypto Officer'
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
  // Read entire certificate contents including secret and key portion.
  'Key Vault Certificate User': subscriptionResourceId(
    'Microsoft.Authorization/roleDefinitions/',
    'db79e9a7-68ee-4b58-9aeb-b90e7c24fcba'
  )
  // Perform any action on the certificates of a key vault, excluding reading the secret and key portions, and managing permissions.
  'Key Vault Certificates Officer': subscriptionResourceId(
    'Microsoft.Authorization/roleDefinitions/',
    'a4417e6f-fecd-4de8-b567-7b0420556985'
  )
  // Perform any action on the keys of a key vault, except manage permissions. Only works for key vaults that use the 'Azure role-based access control' permission model.
  'Key Vault Crypto Officer': subscriptionResourceId(
    'Microsoft.Authorization/roleDefinitions/',
    '14b46e9e-c2b7-41b4-b07b-48a6ebf60603'
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
