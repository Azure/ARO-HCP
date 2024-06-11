@minLength(5)
@maxLength(50)
@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('Location of the registry.')
param location string = resourceGroup().location

@description('Service tier of the Azure Container Registry.')
param acrSku string

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-01-01-preview' = {
  name: acrName
  location: location
  tags: {
    persist: toLower(string(persist))
  }
  sku: {
    name: acrSku
  }
  properties: {
    adminUserEnabled: false
  }
}

@description('Login server property for later use')
output loginServer string = acrResource.properties.loginServer
