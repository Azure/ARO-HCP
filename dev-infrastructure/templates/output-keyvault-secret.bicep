@description('Name of the Key Vault containing the secret')
param keyVaultName string

@description('Name of the secret to inspect')
param secretName string

resource keyVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource secret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' existing = {
  parent: keyVault
  name: secretName
}

output version string = last(split(secret.properties.secretUriWithVersion, '/'))
