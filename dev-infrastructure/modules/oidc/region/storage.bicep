// copied from ARO Pipelines rp/oidc/Region/Templates/modules/storage.bicep

@description('The name of the Azure Storage account to create.')
param accountName string

@description('The location into which the Azure Storage resources should be deployed.')
param location string

@description('The name of the SKU to use when creating the Azure Storage account.')
@allowed([
  'Standard_LRS'
  'Standard_GRS'
  'Standard_ZRS'
  'Standard_GZRS'
  'Premium_LRS'
])
param skuName string

@description('The service principal ID to be added to Azure Storage account.')
param principalId string = ''

@description('Id of the MSI that will be used to run the deploymentScript')
param deploymentMsiId string

// Since deployment script is limted to specific regions, we run deployment script from the same location as the private link.
// The location where deployment script run doesn't matter as it will be removed once the script is completed to enable static website on storage account.
param deploymentScriptLocation string

param allowBlobPublicAccess bool = false

module storageAccount '../../storage/storage.bicep' = {
  name: 'oidcStorageAccount'
  params: {
    storageAccountName: accountName
    location: location
    skuName: skuName
    accessTier: 'Hot'
    allowBlobPublicAccess: allowBlobPublicAccess
    allowSharedKeyAccess: false
    publicNetworkAccess: 'Enabled'
    configureNetworkAcls: false
    configureEncryption: false
  }
}

module storageRbac './storage-setup.bicep' = {
  name: 'oidcStorageRbac'
  params: {
    accountName: accountName
    principalId: principalId
    deploymentMsiId: deploymentMsiId
    deploymentScriptLocation: deploymentScriptLocation
  }
  dependsOn: [
    storageAccount
  ]
}

output storageName string = storageAccount.outputs.storageAccountName
