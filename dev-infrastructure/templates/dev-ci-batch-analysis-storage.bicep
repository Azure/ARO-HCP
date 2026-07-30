@description('Location for the storage account')
param location string

@minLength(3)
@maxLength(24)
@description('Globally-unique name of the storage account that holds Chai-bot Tide-batch analysis output')
param storageAccountName string

@description('Name of the blob container that receives analysis artifacts')
param containerName string

@description('Object (principal) ID of the Chai-bot service principal that reads/writes analysis artifacts')
param chaiPrincipalId string

// Storage Blob Data Contributor: read/write/delete access to blob containers and data (no account-key access).
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#storage-blob-data-contributor
var storageBlobDataContributorRole = 'ba92f5b4-2d11-453d-a403-e96b0029c9fe'

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' = {
  name: storageAccountName
  location: location
  kind: 'StorageV2'
  sku: {
    name: 'Standard_ZRS'
  }
  properties: {
    accessTier: 'Hot'
    supportsHttpsTrafficOnly: true
    allowBlobPublicAccess: false
    allowSharedKeyAccess: false
    minimumTlsVersion: 'TLS1_2'
    // Chai runs outside our network, so it reaches the account over the public
    // endpoint and authenticates with its Entra identity (no shared keys).
    publicNetworkAccess: 'Enabled'
  }
}

resource blobService 'Microsoft.Storage/storageAccounts/blobServices@2023-01-01' = {
  name: 'default'
  parent: storageAccount
}

resource analysisContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-01-01' = {
  name: containerName
  parent: blobService
  properties: {
    publicAccess: 'None'
  }
}

resource chaiBlobContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storageAccount.id, chaiPrincipalId, storageBlobDataContributorRole)
  scope: storageAccount
  properties: {
    principalId: chaiPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageBlobDataContributorRole)
  }
}

output storageAccountName string = storageAccount.name
output blobEndpoint string = storageAccount.properties.primaryEndpoints.blob
