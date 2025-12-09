@description('The name of the Azure Storage account.')
param accountName string

@description('The service principal ID to be added to Azure Storage account.')
param principalIds array

@description('Id of the MSI that will be used to run the deploymentScript')
param deploymentMsiId string

@description('Location where deployment script will run')
param deploymentScriptLocation string

// Storage Account Contributor: Lets you manage storage accounts, including accessing storage account keys which provide full access to storage account data.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/storage#storage-account-contributor
var storageAccountContributorRole = '17d1049b-9a84-46fb-8f53-869881c3d3ab'

// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles#storage-blob-data-contributor
// Storage Blob Data Contributor: Grants access to Read, write, and delete Azure Storage containers and blobs
var storageBlobDataContributorRole = 'ba92f5b4-2d11-453d-a403-e96b0029c9fe'

var scriptToRun = 'storage.sh'

resource storageAccountResource 'Microsoft.Storage/storageAccounts@2023-01-01' existing = {
  name: accountName
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for principalId in principalIds: {
    name: guid(storageAccountResource.id, principalId, storageBlobDataContributorRole)
    scope: storageAccountResource
    properties: {
      principalId: principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageBlobDataContributorRole)
    }
  }
]

resource storageAccountContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storageAccountResource.id, deploymentMsiId, storageAccountContributorRole)
  scope: storageAccountResource
  properties: {
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', storageAccountContributorRole)
  }
}

resource deploymentScript 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: 'deploymentScript'
  location: deploymentScriptLocation
  kind: 'AzureCLI'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${deploymentMsiId}': {}
    }
  }
  properties: {
    azCliVersion: '2.53.1'
    scriptContent: loadTextContent(scriptToRun)
    retentionInterval: 'PT1H'
    environmentVariables: [
      {
        name: 'StorageAccountName'
        value: accountName
      }
    ]
  }
  dependsOn: [
    storageAccountContributor
  ]
}
