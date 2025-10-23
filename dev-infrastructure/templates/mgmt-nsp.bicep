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

@description('ID of the Service Cluster subscription. Will be used to grant access to the NSP.')
param serviceClusterSubscriptionId string

@description('The name of the HCP Backups Storage Account.')
param hcpBackupsStorageAccountName string = ''

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

resource hcpBackupsStorageAccount 'Microsoft.Storage/storageAccounts@2023-05-01' existing = {
  name: hcpBackupsStorageAccountName
}

module nsp '../modules/network/nsp.bicep' = {
  name: 'nsp-${uniqueString(resourceGroup().name)}'
  params: {
    nspName: mgmtNSPName
    location: location
  }
}

// Build the list of associated resources, including the storage account
var associatedResources = [
  cxKeyVault.id
  mgmtKeyVault.id
  msiKeyVault.id
  aksKeyVault.id
  hcpBackupsStorageAccount.id
]

module externalProfile '../modules/network/nsp-profile.bicep' = {
  name: 'profile-${uniqueString(resourceGroup().name)}'
  params: {
    accessMode: mgmtNSPAccessMode
    nspName: mgmtNSPName
    profileName: '${mgmtNSPName}-profile'
    location: location
    associatedResources: associatedResources
    // TODO: will add EV2 Service Tags here
    // TODO: add service cluster subscription here
    subscriptions: [
      serviceClusterSubscriptionId
      subscription().id
    ]
  }
  dependsOn: [
    nsp
  ]
}
