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
param principalIds array

@description('Id of the MSI that will be used to run the deploymentScript')
param deploymentMsiId string

// Since deployment script is limted to specific regions, we run deployment script from the same location as the private link.
// The location where deployment script run doesn't matter as it will be removed once the script is completed to enable static website on storage account.
param deploymentScriptLocation string

param allowBlobPublicAccess bool = false

@description('The name of the storage account used by deployment scripts (must have allowSharedKeyAccess=false and MI granted Storage File Data Privileged Contributor)')
param deploymentScriptStorageAccountName string = ''

@description('The subnet ID for the deployment scripts ACI container (required when using MI-auth storage)')
param deploymentScriptSubnetId string = ''

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
    principalIds: principalIds
    deploymentMsiId: deploymentMsiId
    deploymentScriptLocation: deploymentScriptLocation
    deploymentScriptStorageAccountName: deploymentScriptStorageAccountName
    deploymentScriptSubnetId: deploymentScriptSubnetId
  }
  dependsOn: [
    storageAccount
  ]
}

output storageName string = storageAccount.outputs.storageAccountName
