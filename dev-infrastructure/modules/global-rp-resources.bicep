param globalMSIName string
param acrName string

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: globalMSIName
}

resource acr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrName
}

output globalMSIId string = globalMSI.id
output acrLoginServer string = acr.properties.loginServer
