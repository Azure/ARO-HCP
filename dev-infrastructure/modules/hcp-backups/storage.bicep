@description('The name of the Azure Storage account to create.')
@minLength(3)
@maxLength(24)
param storageAccountName string

@description('The location into which the Azure Storage resources should be deployed.')
param location string

@description('The name of the blob container to create.')
param containerName string = 'backups'

// Storage Account for HCP Backups
// Configured using MSFTs recommended workload https://docs.azure.cn/en-us/storage/common/storage-account-overview#recommended-workload-configurations
resource hcpBackupsStorageAccount 'Microsoft.Storage/storageAccounts@2022-09-01' = {
  name: storageAccountName
  location: location
  kind: 'StorageV2'
  sku: {
    name: 'Standard_ZRS'
  }
  properties: {
    accessTier: 'Cool'
    minimumTlsVersion: 'TLS1_2'
    allowBlobPublicAccess: false
    supportsHttpsTrafficOnly: true
    encryption: {
      services: {
        blob: {
          enabled: true
        }
        file: {
          enabled: true
        }
      }
      keySource: 'Microsoft.Storage'
    }
    networkAcls: {
      bypass: 'AzureServices'
      defaultAction: 'Allow'
    }
  }
}

resource blobService 'Microsoft.Storage/storageAccounts/blobServices@2022-09-01' = {
  name: 'default'
  parent: hcpBackupsStorageAccount
}

resource hcpBackupsContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2022-09-01' = {
  name: containerName
  parent: blobService
}

output storageAccountId string = hcpBackupsStorageAccount.id
output storageAccountName string = hcpBackupsStorageAccount.name
output containerName string = hcpBackupsContainer.name
