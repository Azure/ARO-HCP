param location string

param storageAccountName string
param globalMSIName string

resource relasePublisher 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'release-publisher'
  location: location
}

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

// Storage deployment
module storageAccount '../modules/storage/account.bicep' = {
  name: 'storage'
  params: {
    accountName: storageAccountName
    location: location
    principalId: relasePublisher.properties.principalId
    skuName: 'Standard_ZRS'
    deploymentMsiId: globalMSI.id
    deploymentScriptLocation: location
    allowBlobPublicAccess: true
  }
}
