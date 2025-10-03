@description('The name of the Maestro Server MSI')
param maestroMsiName string

@description('The name of the Image Puller MSI')
param imagePullerMsiName string

//
//   M A E S T R O   S E R V E R   L O O K U P
//

resource maestroIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: maestroMsiName
}

output tenantId string = tenant().tenantId
output maestroMsiClientId string = maestroIdentity.properties.clientId

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId
