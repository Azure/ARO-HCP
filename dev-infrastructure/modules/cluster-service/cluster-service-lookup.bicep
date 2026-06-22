@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the Cluster Service MSI')
param csMsiName string

//
//   I M A G E   P U L L E R   L O O K U P
//

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId

//
//   C L U S T E R   S E R V I C E   L O O K U P
//

resource csIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: csMsiName
}

output tenantId string = tenant().tenantId
output csMsiClientId string = csIdentity.properties.clientId
