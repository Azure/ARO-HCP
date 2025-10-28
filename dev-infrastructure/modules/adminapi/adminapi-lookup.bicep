@description('The name of the AdminAPI MSI')
param adminApiMsiName string

@description('The name of the Image Puller MSI')
param imagePullerMsiName string

//
//   A D M I N   A P I   L O O K U P
//

resource adminApiIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: adminApiMsiName
}

output tenantId string = tenant().tenantId
output adminApiMsiClientId string = adminApiIdentity.properties.clientId

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId
