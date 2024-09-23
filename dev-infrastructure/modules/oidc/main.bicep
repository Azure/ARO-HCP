// partially copied from ARO Pipelines rp/oidc/Region/Templates/main.bicep

param location string
param storageAccountName string
param rpMsiName string
param skuName string
param aroDevopsMsiId string
param deploymentScriptLocation string
param enabledAFD bool = false

var rpMsiResourceURI = resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', rpMsiName)

// Storage deployment
module storageAccount 'storage.bicep' = {
  name: 'storage'
  params: {
    accountName: storageAccountName
    location: location
    principalId: reference(rpMsiResourceURI, '2023-01-31').principalId
    skuName: skuName
    aroDevopsMsiId: aroDevopsMsiId
    deploymentScriptLocation: deploymentScriptLocation
    publicBlobAccess: !enabledAFD
  }
}

// Custom Domain, Route, Origin deployment and storage endpoint have been removed
// because it is not required for the time being while testing in DEV only
