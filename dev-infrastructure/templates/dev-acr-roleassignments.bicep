@minLength(5)
@maxLength(40)
@description('Globally unique name of the Azure Container Registry')
param acrName string

module acrPush '../modules/acr-push-permission.bicep' = {
  name: guid(acrName, 'imagesync', 'push')
  params: {
    principalName: 'image-sync'
  }
}
