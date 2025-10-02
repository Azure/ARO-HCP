@description('The name of the Image Puller MSI')
param imagePullerMsiName string

//
//   I M A G E   P U L L E R   L O O K U P
//

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId
output imagePullerMsiTenantId string = imagePullerIdentity.properties.tenantId
