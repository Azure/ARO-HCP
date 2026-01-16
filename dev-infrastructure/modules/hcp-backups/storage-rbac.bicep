@description('Storage account name for HCP backups')
param storageAccountName string

@description('Principal ID of the Velero managed identity')
param veleroManagedIdentityPrincipalId string

// Storage Blob Data Contributor: Grants read, write, and delete access to blob containers and data
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#storage-blob-data-contributor
var storageBlobDataContributorRole = 'ba92f5b4-2d11-453d-a403-e96b0029c9fe'

// Storage Account Key Operator Service Role: Grants permission to list and regenerate storage account keys
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#storage-account-key-operator-service-role
var storageAccountKeyOperatorRole = '81a9662b-bebf-436f-a333-f67b29880f12'

// Reader: Grants permission to read storage account properties
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#reader
var readerRole = 'acdd72a7-3385-48ef-bd42-f606fba81ae7'

resource hcpBackupsStorageAccount 'Microsoft.Storage/storageAccounts@2022-09-01' existing = {
  name: storageAccountName
}

// ============================================================================
// Velero Managed Identity - Role Assignments
// Roles: Storage Blob Data Contributor, Storage Account Key Operator, Reader
// ============================================================================

resource veleroStorageBlobDataContributorAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storageAccountName, 'velero-blob-contributor', storageBlobDataContributorRole)
  scope: hcpBackupsStorageAccount
  properties: {
    principalId: veleroManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageBlobDataContributorRole)
  }
}

resource veleroStorageAccountKeyOperatorAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storageAccountName, 'velero-key-operator', storageAccountKeyOperatorRole)
  scope: hcpBackupsStorageAccount
  properties: {
    principalId: veleroManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageAccountKeyOperatorRole)
  }
}

resource veleroReaderAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storageAccountName, 'velero-reader', readerRole)
  scope: hcpBackupsStorageAccount
  properties: {
    principalId: veleroManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', readerRole)
  }
}
