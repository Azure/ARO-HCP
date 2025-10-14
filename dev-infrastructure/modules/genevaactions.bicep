@description('Location of the keyvault.')
param location string

@description('Name of the key vault.')
param genevaKeyVaultName string
@description('Should the geneva actions keyvault be private')
param genevaKeyVaultPrivate bool
@description('Should the geneva actions keyvault have soft delete enabled')
param genevaKeyVaultSoftDelete bool
@description('Tag key for the geneva actions keyvault')
param genevaKeyVaultTagKey string
@description('Tag value for the geneva actions keyvault')
param genevaKeyVaultTagValue string
@description('Name of geneva action extensions')
param allowedAcisExtensions string
@description('App ID for Geneva Actions')
param genevaActionsPrincipalId string


//   G E N E V A    K V

module genevaKv '../modules/keyvault/keyvault.bicep' = {
  name: 'geneva-kv'
  params: {
    location: location
    keyVaultName: genevaKeyVaultName
    private: genevaKeyVaultPrivate
    enableSoftDelete: genevaKeyVaultSoftDelete
    tagKey: genevaKeyVaultTagKey
    tagValue: genevaKeyVaultTagValue
  }
}

output genevaKeyVaultUrl string = genevaKv.outputs.kvUrl

module genevaKvSecretsUserAccessToGenevaApp '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(genevaKeyVaultName, 'KeyVaultAccess', 'Key Vault Secrets User', genevaActionsPrincipalId)
  params: {
    keyVaultName: genevaKv.outputs.kvName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: genevaActionsPrincipalId
  }
}

module genevaKvReaderAccessToGenevaApp '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(genevaKeyVaultName, 'KeyVaultAccess', 'Key Vault Reader', genevaActionsPrincipalId)
  params: {
    keyVaultName: genevaKv.outputs.kvName
    roleName: 'Key Vault Reader'
    managedIdentityPrincipalId: genevaActionsPrincipalId
  }
}

resource allowedExtensionsSecret 'Microsoft.KeyVault/vaults/secrets@2021-04-01-preview' = {
  name: '${genevaKeyVaultName}/AllowedAcisExtensions'
  properties: {
    value: allowedAcisExtensions
  }
  dependsOn: [
    genevaKv
  ]
}
