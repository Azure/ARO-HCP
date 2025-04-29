@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('Naming prefixes for mgmt NSPs')
param mgmtNSPNamePrefix string

@description('Access mode for this NSP')
@allowed(['Audit', 'Enforced', 'Learning'])
param mgmtNSPAccessMode string

@description('The name of the keyvault for AKS.')
@maxLength(24)
param aksKeyVaultName string

resource mgmtKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: mgmtKeyVaultName
}

resource cxKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: cxKeyVaultName
}

resource msiKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: msiKeyVaultName
}

resource aksKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: aksKeyVaultName
}

module externalNSP '../modules/network/nsp.bicep' = {
  name: 'nsp-${uniqueString(resourceGroup().name)}-external'
  params: {
    accessMode: mgmtNSPAccessMode
    nspName: '${mgmtNSPNamePrefix}-external'
    location: location
    associatedResources: [
      cxKeyVault.id
      msiKeyVault.id
    ]
    // TODO: will add EV2 Service Tags here
    subscriptions: [
      subscription().id
    ]
  }
}

module internalNSP '../modules/network/nsp.bicep' = {
  name: 'nsp-${uniqueString(resourceGroup().name)}-internal'
  params: {
    accessMode: mgmtNSPAccessMode
    nspName: '${mgmtNSPNamePrefix}-internal'
    location: location
    associatedResources: [
      mgmtKeyVault.id
      aksKeyVault.id
    ]
    subscriptions: [
      subscription().id
    ]
  }
}
