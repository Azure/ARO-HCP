@description('The name of the AROBit Secret Provider MSI')
param msiName string

@description('The resource group containing the AROBit Secret Provider MSI in this subscription')
param infrastructureIdentityResourceGroup string = resourceGroup().name

//
//   A R O B I T   L O O K U P
//

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(infrastructureIdentityResourceGroup)
  name: msiName
}

output tenantId string = tenant().tenantId
output msiClientId string = managedIdentity.properties.clientId
