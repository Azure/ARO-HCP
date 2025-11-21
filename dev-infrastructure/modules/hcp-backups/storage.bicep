import {
  csvToArray
  determineZoneRedundancy
  getLocationAvailabilityZonesCSV
} from '../common.bicep'

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

@description('The name of the Azure Storage account to create.')
@minLength(3)
@maxLength(24)
param storageAccountName string

@description('The location into which the Azure Storage resources should be deployed.')
param location string

@description('The name of the blob container to create.')
param containerName string = 'backups'

@description('Zone redundant mode for the storage account.')
param zoneRedundantMode string = 'Auto'

@description('Whether the storage account should allow public access.')
param public bool = false

// Storage Account for HCP Backups
// Configured using MSFTs recommended workload https://docs.azure.cn/en-us/storage/common/storage-account-overview#recommended-workload-configurations
module hcpBackupsStorageAccount '../storage/storage.bicep' = {
  name: 'hcpBackupsStorageAccount'
  params: {
    storageAccountName: storageAccountName
    location: location
    skuName: determineZoneRedundancy(locationAvailabilityZoneList, zoneRedundantMode) ? 'Standard_ZRS' : 'Standard_LRS'
    accessTier: 'Cool'
    allowBlobPublicAccess: public
    allowSharedKeyAccess: true
    publicNetworkAccess: 'Enabled'
    configureNetworkAcls: true
    networkAclsBypass: 'AzureServices'
    networkAclsDefaultAction: 'Allow'
    configureEncryption: true
  }
}

resource blobService 'Microsoft.Storage/storageAccounts/blobServices@2022-09-01' = {
  name: '${toLower(storageAccountName)}/default'
  dependsOn: [
    hcpBackupsStorageAccount
  ]
}

resource hcpBackupsContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2022-09-01' = {
  name: containerName
  parent: blobService
}

output storageAccountId string = hcpBackupsStorageAccount.outputs.storageAccountId
output storageAccountName string = hcpBackupsStorageAccount.outputs.storageAccountName
output containerName string = containerName
