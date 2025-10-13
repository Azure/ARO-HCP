param location string

param storageAccountName string

resource relasePublisher 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'release-publisher'
  location: location
}

// https://www.azadvertizer.net/azrolesadvertizer/b7e6dc6d-f1e8-4753-8033-0f276bb0955b.html
// Storage Blob Data Owner: Allows for full access to Azure Storage blob containers and data, including assigning POSIX access control.
var storageBlobDataOwnerRole = 'b7e6dc6d-f1e8-4753-8033-0f276bb0955b'

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
  name: guid(storageAccount.id, relasePublisher.id, storageBlobDataOwnerRole)
  scope: storageAccount
  properties: {
    principalId: relasePublisher.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageBlobDataOwnerRole)
  }
}
