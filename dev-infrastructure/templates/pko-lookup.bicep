@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the PKO MSI')
param pkoMsiName string

//
//   I M A G E   P U L L E R   L O O K U P
//

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId
output imagePullerMsiTenantId string = imagePullerIdentity.properties.tenantId

//
//   P K O   L O O K U P
//

resource pkoIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: pkoMsiName
}

output pkoMsiClientId string = pkoIdentity.properties.clientId
output pkoMsiTenantId string = pkoIdentity.properties.tenantId
