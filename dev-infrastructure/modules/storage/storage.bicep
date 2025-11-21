// Storage Account Module
// This module deploys ONLY the storage account resource itself
// Child resources (blob services, containers, RBAC, etc.) should be managed by calling modules

@minLength(3)
@maxLength(24)
@description('The name of the storage account')
param storageAccountName string

@description('The location for the storage account')
param location string

@description('The SKU name for the storage account')
param skuName string = 'Standard_ZRS'

@description('The access tier for the storage account')
@allowed(['Hot', 'Cool'])
param accessTier string = 'Hot'

@description('Whether to allow blob public access')
param allowBlobPublicAccess bool = false

@description('Whether to allow shared key access')
param allowSharedKeyAccess bool = true

@description('Public network access setting')
@allowed(['Enabled', 'Disabled'])
param publicNetworkAccess string = 'Enabled'

@description('Whether to configure network ACLs')
param configureNetworkAcls bool = false

@description('Network ACLs bypass setting')
param networkAclsBypass string = 'AzureServices'

@description('Network ACLs default action')
@allowed(['Allow', 'Deny'])
param networkAclsDefaultAction string = 'Allow'

@description('Whether to explicitly configure encryption (blob and file services)')
param configureEncryption bool = false

var baseProperties = {
  accessTier: accessTier
  minimumTlsVersion: 'TLS1_2'
  allowBlobPublicAccess: allowBlobPublicAccess
  supportsHttpsTrafficOnly: true
  allowSharedKeyAccess: allowSharedKeyAccess
  publicNetworkAccess: publicNetworkAccess
}

// Optional network ACLs configuration
var networkAclsConfig = configureNetworkAcls
  ? {
      networkAcls: {
        bypass: networkAclsBypass
        defaultAction: networkAclsDefaultAction
      }
    }
  : {}

// Optional encryption configuration
var encryptionConfig = configureEncryption
  ? {
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
    }
  : {}

var storageProperties = union(baseProperties, networkAclsConfig, encryptionConfig)

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' = {
  name: toLower(storageAccountName)
  location: location
  kind: 'StorageV2'
  sku: {
    name: skuName
  }
  properties: storageProperties
}

output storageAccountId string = storageAccount.id
output storageAccountName string = storageAccount.name
