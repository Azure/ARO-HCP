@description('The name of the storage account for deployment scripts')
@minLength(3)
@maxLength(24)
param deploymentScriptStorageAccountName string

@description('The MSI resource ID whose principal will be granted access to the storage account')
param globalMSIId string

param location string = resourceGroup().location

module deploymentScriptStorage '../modules/deployment-script-storage.bicep' = {
  name: 'deployment-script-storage'
  params: {
    storageAccountName: deploymentScriptStorageAccountName
    location: location
    managedIdentityPrincipalIds: [
      reference(globalMSIId, '2023-01-31').principalId
    ]
  }
}
