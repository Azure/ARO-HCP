@minLength(5)
@maxLength(40)
@description('Globally unique name of the Azure Container Registry')
param acrName string

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'image-sync'
}

module acrPush '../modules/acr-permissions.bicep' = {
  name: guid(acrName, 'imagesync', 'push')
  params: {
    principalId: uami.properties.principalId
    grantPushAccess: true
  }
}
