// partially copied from ARO Pipelines rp/oidc/Region/Templates/main.bicep

param location string
param storageAccountName string
param rpMsiName string
param skuName string
param msiId string
param deploymentScriptLocation string
param enabledAFD bool = false
param regionalResourceGroup string = resourceGroup().name

var rpMsiResourceURI = resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', rpMsiName)

// Storage deployment
module storageAccount 'storage.bicep' = {
  name: 'storage'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    accountName: storageAccountName
    location: location
    principalId: reference(rpMsiResourceURI, '2023-01-31').principalId
    skuName: skuName
    msiId: msiId
    deploymentScriptLocation: deploymentScriptLocation
    isDevEnv: !enabledAFD
  }
}

// Custom Domain, Route, Origin deployment and storage endpoint have been removed
// because it is not required for the time being while testing in DEV only
