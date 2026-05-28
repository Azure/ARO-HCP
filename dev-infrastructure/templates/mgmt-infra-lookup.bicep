@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

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

module msiKeyVault '../modules/keyvault/lookup.bicep' = {
  name: 'msi-kv-${uniqueString(msiKeyVaultName)}'
  params: {
    keyVaultName: msiKeyVaultName
  }
}

output msiKeyVaultName string = msiKeyVault.outputs.keyVaultName
output msiKeyVaultUrl string = msiKeyVault.outputs.keyVaultUrl
