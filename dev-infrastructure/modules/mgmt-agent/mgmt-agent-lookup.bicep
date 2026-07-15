@description('The name of the mgmt-agent MSI')
param msiName string

//
//   M G M T   A G E N T   L O O K U P
//

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: msiName
}

output tenantId string = tenant().tenantId
output msiClientId string = managedIdentity.properties.clientId
output msiPrincipalId string = managedIdentity.properties.principalId
