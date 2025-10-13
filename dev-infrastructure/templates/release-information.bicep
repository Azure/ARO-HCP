param location string

param storageAccountName string

resource relasePublisher 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'release-publisher'
  location: location
}

// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#storage-blob-data-contributor
// Storage Blob Data Contributor: Grants access to Read, write, and delete Azure Storage containers and blobs
var storageBlobDataContributorRole = 'ba92f5b4-2d11-453d-a403-e96b0029c9fe'

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' = {
  location: location
  name: storageAccountName
  kind: 'StorageV2'
  sku: {
    name: 'Standard_ZRS'
  }
  properties: {
    accessTier: 'Hot'
    supportsHttpsTrafficOnly: true
    allowBlobPublicAccess: false
    minimumTlsVersion: 'TLS1_2'
    allowSharedKeyAccess: false
    publicNetworkAccess: 'Enabled'
  }
}

// blob service
resource blobService 'Microsoft.Storage/storageAccounts/blobServices@2023-01-01' = {
  name: 'default'
  parent: storageAccount
}

// blob container for release data

resource releaseInfoContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-01-01' = {
  name: 'releases'
  parent: blobService
  properties: {
    publicAccess: 'None'
  }
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storageAccount.id, relasePublisher.id, storageBlobDataContributorRole)
  scope: storageAccount
  properties: {
    principalId: relasePublisher.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageBlobDataContributorRole)
  }
}
