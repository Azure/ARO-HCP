@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

module mgmtKeyVault '../modules/keyvault/lookup.bicep' = {
  name: 'mgmt-kv-${uniqueString(mgmtKeyVaultName)}'
  params: {
    keyVaultName: mgmtKeyVaultName
  }
}

output mgmtKeyVaultName string = mgmtKeyVault.outputs.keyVaultName
output mgmtKeyVaultUrl string = mgmtKeyVault.outputs.keyVaultUrl

module cxKeyVault '../modules/keyvault/lookup.bicep' = {
  name: 'cx-kv-${uniqueString(cxKeyVaultName)}'
  params: {
    keyVaultName: cxKeyVaultName
  }
}

output cxKeyVaultName string = cxKeyVault.outputs.keyVaultName
output cxKeyVaultUrl string = cxKeyVault.outputs.keyVaultUrl
