@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('Name of the mgmt NSPs')
param mgmtNSPName string

@description('Access mode for this NSP')
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

module nsp '../modules/network/nsp.bicep' = {
  name: 'nsp-${uniqueString(resourceGroup().name)}'
  params: {
    nspName: mgmtNSPName
    location: location
  }
}

module externalProfile '../modules/network/nsp-profile.bicep' = {
  name: 'profile-${uniqueString(resourceGroup().name)}'
  params: {
    accessMode: mgmtNSPAccessMode
    nspName: mgmtNSPName
    profileName: '${mgmtNSPName}-profile'
    location: location
    associatedResources: [
      cxKeyVault.id
      mgmtKeyVault.id
      msiKeyVault.id
      aksKeyVault.id
    ]
    // TODO: will add EV2 Service Tags here
    // TODO: add service cluster subscription here
    subscriptions: [
      subscription().id
    ]
  }
  dependsOn: [
    nsp
  ]
}
